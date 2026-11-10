/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package clusterinstance

import (
	"bytes"
	"context"
	"fmt"
	"text/template"
	"unicode"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

const (
	WaveAnnotation        = v1alpha1.Group + "/sync-wave"
	DefaultWaveAnnotation = "0"
)

type TemplateEngine struct{}

// renderScope identifies the scope a set of templates renders in. The cluster
// is always set; exactly one of node or group may also be set. When node is
// set it holds the node's merged (effective) NodeSpec, so group-inherited
// values are already applied. When group is set the scope is the group itself
// and templates render once per group. State carries values derived once per
// reconciliation pass; it may be nil for direct tests, in which case it is
// computed on demand.
type renderScope struct {
	cluster *v1alpha1.ClusterInstance
	node    *v1alpha1.NodeSpec
	group   *v1alpha1.NodeGroupSpec
	state   *renderState
}

func NewTemplateEngine() *TemplateEngine {
	return &TemplateEngine{}
}

func (te *TemplateEngine) ProcessTemplates(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance v1alpha1.ClusterInstance,
) (RenderedObjectCollection, error) {

	log = log.Named("ProcessTemplates")

	// Derive the values shared by every rendered template once. This keeps
	// rendering linear in the number of nodes: each node's effective spec is
	// merged once instead of once per rendered template.
	state := newRenderState(&clusterInstance)

	var renderedObjects RenderedObjectCollection
	log.Info("Started processing cluster-level install templates")

	// Render cluster-level install templates
	clusterObjects, err := te.renderTemplates(ctx, c, log, renderScope{
		cluster: &clusterInstance,
		state:   state,
	})
	if err != nil {
		log.Error("Encountered error while processing cluster-level install templates", zap.Error(err))
		return renderedObjects, err
	}

	if err := renderedObjects.AddObjects(clusterObjects); err != nil {
		return renderedObjects, err
	}
	log.Info("Finished processing cluster-level install templates")

	// Process group-level install templates
	numGroups := len(clusterInstance.Spec.Groups)
	for groupId, group := range clusterInstance.Spec.Groups {
		log.Sugar().Infof("Started processing group-level install templates [group: %d of %d]", groupId+1, numGroups)
		groupCopy := group

		// Render group-level templates
		groupObjects, err := te.renderTemplates(ctx, c, log, renderScope{
			cluster: &clusterInstance,
			group:   &groupCopy,
			state:   state,
		})
		if err != nil {
			log.Sugar().Errorf(
				"Encountered error while processing group-level install templates [group: %d of %d], err: %v",
				groupId+1, numGroups, err)
			return renderedObjects, err
		}

		if err := renderedObjects.AddObjects(groupObjects); err != nil {
			return renderedObjects, err
		}

		log.Sugar().Infof("Finished processing group-level install templates [group: %d of %d]", groupId+1, numGroups)
	}

	// Process node-level install templates. Every node renders with its
	// effective spec: group-inherited values merged in and rendering defaults
	// applied. The effective specs were computed once in state.
	numNodes := len(clusterInstance.Spec.Nodes)
	for nodeId := range clusterInstance.Spec.Nodes {
		log.Sugar().Infof("Started processing node-level install templates [node: %d of %d]", nodeId+1, numNodes)
		nodeCopy := state.effectiveNodes[nodeId]

		// Render node-level templates
		nodeObjects, err := te.renderTemplates(ctx, c, log, renderScope{
			cluster: &clusterInstance,
			node:    &nodeCopy,
			state:   state,
		})
		if err != nil {
			log.Sugar().Errorf(
				"Encountered error while processing node-level install templates [node: %d of %d], err: %v",
				nodeId+1, numNodes, err)
			return renderedObjects, err
		}

		if err := renderedObjects.AddObjects(nodeObjects); err != nil {
			return renderedObjects, err
		}

		log.Sugar().Infof("Finished processing node-level install templates [node: %d of %d]", nodeId+1, numNodes)
	}

	return renderedObjects, nil
}

func (te *TemplateEngine) renderTemplates(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	scope renderScope,
) ([]RenderedObject, error) {

	var (
		renderedObjects []RenderedObject
		templateRefs    []v1alpha1.TemplateRef
	)

	// Determine which templateRefs apply to this render scope
	switch {
	case scope.node != nil:
		// use node-level values (merged with any group nodeSpec values)
		templateRefs = scope.node.TemplateRefs
	case scope.group != nil:
		// use group-level values
		templateRefs = scope.group.GroupTemplateRefs
	default:
		// use cluster-level values
		templateRefs = scope.cluster.Spec.TemplateRefs
	}

	log = log.Named("renderTemplates")

	for tId, templateRef := range templateRefs {
		log.Sugar().Infof("Processing templateRef %d of %d", tId+1, len(templateRefs))

		templatesConfigMap := &corev1.ConfigMap{}
		if err := c.Get(ctx, types.NamespacedName{
			Name:      templateRef.Name,
			Namespace: templateRef.Namespace,
		}, templatesConfigMap); err != nil {
			log.Error("Failed to get ConfigMap", zap.Error(err))
			return renderedObjects, fmt.Errorf("failed to get ConfigMap %s/%s: %w", templateRef.Namespace, templateRef.Name, err)
		}

		// process Template ConfigMap
		for templateKey, template := range templatesConfigMap.Data {

			object, err := te.renderManifestFromTemplate(
				ctx,
				c,
				log,
				scope,
				templateRef.Name,
				templateKey,
				template)
			if err != nil {
				return renderedObjects, err
			}
			renderedObjects = append(renderedObjects, object)
		}
	}
	return renderedObjects, nil
}

func appendAnnotationsAndLabels(
	scope renderScope,
	manifest map[string]interface{},
	kind string,
) map[string]interface{} {
	switch {
	case scope.node != nil:
		// Append node-level user provided extra annotations if exist
		if extraManifestAnnotations, ok := scope.node.ExtraAnnotationSearch(kind, &scope.cluster.Spec); ok {
			manifest = appendManifestAnnotations(extraManifestAnnotations, manifest)
		}

		// Append node-level user provided extra labels if exist
		if extraManifestLabels, ok := scope.node.ExtraLabelSearch(kind, &scope.cluster.Spec); ok {
			manifest = appendManifestLabels(extraManifestLabels, manifest)
		}
	case scope.group != nil:
		// The group is the most specific scope for objects rendered from its
		// groupTemplateRefs. As at the node scope, a Kind defined at this
		// scope replaces the cluster-level map for that Kind; the cluster map
		// is only a fallback for Kinds the group does not define. Values
		// already in the template are never overwritten.
		if extraManifestAnnotations, ok := scope.group.ExtraAnnotations[kind]; ok {
			manifest = appendManifestAnnotations(extraManifestAnnotations, manifest)
		} else if extraManifestAnnotations, ok := scope.cluster.Spec.ExtraAnnotationSearch(kind); ok {
			manifest = appendManifestAnnotations(extraManifestAnnotations, manifest)
		}

		if extraManifestLabels, ok := scope.group.ExtraLabels[kind]; ok {
			manifest = appendManifestLabels(extraManifestLabels, manifest)
		} else if extraManifestLabels, ok := scope.cluster.Spec.ExtraLabelSearch(kind); ok {
			manifest = appendManifestLabels(extraManifestLabels, manifest)
		}
	default:
		// Append cluster-level user provided extra annotations if exist
		if extraManifestAnnotations, ok := scope.cluster.Spec.ExtraAnnotationSearch(kind); ok {
			manifest = appendManifestAnnotations(extraManifestAnnotations, manifest)
		}

		// Append cluster-level user provided extra labels if exist
		if extraManifestLabels, ok := scope.cluster.Spec.ExtraLabelSearch(kind); ok {
			manifest = appendManifestLabels(extraManifestLabels, manifest)
		}
	}
	return manifest
}

func (te *TemplateEngine) renderManifestFromTemplate(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	scope renderScope,
	templateRefName, templateKey, template string,
) (RenderedObject, error) {

	var object RenderedObject

	log = log.Named("renderManifestFromTemplate")

	clusterData, err := buildClusterData(ctx, c, scope)
	if err != nil {
		log.Error("Failed to build ClusterInstance data", zap.Error(err))
		return object, err
	}

	manifest, err := te.render(templateKey, template, clusterData)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to render templateRef %s", templateRefName), zap.Error(err))
		return object, err
	}
	if manifest == nil {
		return object, nil
	}

	if err := object.SetObject(manifest); err != nil {
		log.Error(fmt.Sprintf("Failed to parse rendered template templateRef %s", templateRefName), zap.Error(err))
		return object, err
	}

	apiVersion := object.GetAPIVersion()
	kind := object.GetKind()
	name := object.GetName()
	namespace := object.GetNamespace()

	// Default action is to render the manifest
	object.action = actionRender

	// Determine if manifest should be pruned or suppressed. Group-scope
	// rendering only consults the cluster-level lists; node-scope rendering
	// uses the merged node lists so group-inherited entries apply.
	suppressManifestLogMsg := fmt.Sprintf("Suppressed manifest %s", GetResourceId(name, namespace, kind))
	pruneList := scope.cluster.Spec.PruneManifests
	suppressManifestsList := scope.cluster.Spec.SuppressedManifests
	if scope.node != nil {
		pruneList = append(pruneList, scope.node.PruneManifests...)
		suppressManifestsList = append(suppressManifestsList, scope.node.SuppressedManifests...)
	}
	if pruneManifest(v1alpha1.ResourceRef{APIVersion: apiVersion, Kind: kind}, pruneList) {
		object.action = actionPrune
		log.Debug(suppressManifestLogMsg)
		return object, nil
	}
	if suppressManifest(kind, suppressManifestsList) {
		object.action = actionSuppress
		log.Debug(suppressManifestLogMsg)
		return object, nil
	}

	// Append Annotations and Labels to rendered manifest
	updatedManifest := appendAnnotationsAndLabels(scope, object.GetObject().Object, kind)

	// Add owned-by label
	updatedManifest = appendManifestLabels(map[string]string{
		OwnedByLabel: GenerateOwnedByLabelValue(scope.cluster.Namespace, scope.cluster.Name),
	}, updatedManifest)

	// Update the rendered object with the labels and annotations applied above
	if err := object.SetObject(updatedManifest); err != nil {
		log.Error(fmt.Sprintf("Failed to parse rendered template templateRef %s", templateRefName), zap.Error(err))
		return object, err
	}

	return object, nil
}

func validateRenderedTemplate(manifest map[string]interface{}, templateKey string) error {
	if _, ok := manifest["apiVersion"].(string); !ok {
		return fmt.Errorf("missing apiVersion in template %s", templateKey)
	}

	if _, ok := manifest["kind"].(string); !ok {
		return fmt.Errorf("missing kind in template %s", templateKey)
	}

	if metadata, ok := manifest["metadata"].(map[string]interface{}); ok {
		if _, ok := metadata["name"].(string); !ok {
			return fmt.Errorf("missing metadata.name in template %s", templateKey)
		}
	} else {
		return fmt.Errorf("missing metadata in template %s", templateKey)
	}

	// all validations passed
	return nil
}

func ParseTemplate(templateKey, templateStr string) (*template.Template, error) {
	fMap := funcMap()
	t, err := template.New(templateKey).Funcs(fMap).Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}
	return t, nil
}

func (te *TemplateEngine) render(templateKey, templateStr string, data *ClusterData) (map[string]interface{}, error) {

	renderedTemplate := make(map[string]interface{})
	t, err := ParseTemplate(templateKey, templateStr)
	if err != nil {
		return nil, fmt.Errorf("encountered an error parsing template %s: %w", templateKey, err)
	}

	var buffer bytes.Buffer
	err = t.Execute(&buffer, data)
	if err != nil {
		return nil, fmt.Errorf("failed to execute template %s: %w", templateKey, err)
	}

	// Ensure there's non-whitespace content
	for _, r := range buffer.String() {
		if !unicode.IsSpace(r) {
			if err := yaml.Unmarshal(buffer.Bytes(), &renderedTemplate); err != nil {
				return renderedTemplate, fmt.Errorf("failed to unmarshal YAML: %w", err)
			}
			return renderedTemplate, validateRenderedTemplate(renderedTemplate, templateKey)
		}
	}

	// Output is all whitespace; return nil instead
	return nil, nil
}
