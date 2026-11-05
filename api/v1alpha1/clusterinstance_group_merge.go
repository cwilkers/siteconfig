/*
Copyright 2025.

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

package v1alpha1

import (
	"maps"

	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

// MergeNodeSpec returns the effective NodeSpec for a node, applying
// nodes[] > groups[].nodeSpec > .spec precedence with per-field strategies:
// scalar fields resolve to the most specific scope that sets them, pointers
// prefer the node value, maps are unioned by key with the node value winning
// on collision, and lists are unioned with group entries first. Identity
// fields (hostName, bmcAddress, bootMACAddress, bmcCredentialsName, hostRef,
// nodeNetwork, group) are always taken from the node.
//
// Because the API server persists CRD defaults, a node value that equals its
// CRD default cannot be distinguished from a defaulted one; when the group
// supplies a different value, a defaulted node value is treated as
// unspecified. A node that explicitly wants the default value while its group
// wants something else must be moved out of the group or use a custom
// template.
func MergeNodeSpec(group *GroupNodeSpec, node NodeSpec) NodeSpec {
	if group == nil {
		return node
	}

	merged := node

	merged.Role = mergeScalar("role", node.Role, group.Role)
	merged.CPUArchitecture = CPUArchitecture(
		mergeScalar("cpuArchitecture", string(node.CPUArchitecture), string(group.CPUArchitecture)))
	merged.BootMode = mergeScalarCast[bmh_v1alpha1.BootMode]("bootMode", string(node.BootMode), string(group.BootMode))
	merged.AutomatedCleaningMode = mergeScalarCast[bmh_v1alpha1.AutomatedCleaningMode](
		"automatedCleaningMode", string(node.AutomatedCleaningMode), string(group.AutomatedCleaningMode))
	merged.IronicInspect = mergeScalarCast[IronicInspect](
		"ironicInspect", string(node.IronicInspect), string(group.IronicInspect))
	merged.InstallerArgs = mergeScalar("installerArgs", node.InstallerArgs, group.InstallerArgs)
	merged.IgnitionConfigOverride = mergeScalar(
		"ignitionConfigOverride", node.IgnitionConfigOverride, group.IgnitionConfigOverride)

	if node.RootDeviceHints == nil {
		merged.RootDeviceHints = group.RootDeviceHints
	}

	merged.NodeLabels = unionStringMap(node.NodeLabels, group.NodeLabels)
	merged.ExtraAnnotations = unionKindMap(node.ExtraAnnotations, group.ExtraAnnotations)
	merged.ExtraLabels = unionKindMap(node.ExtraLabels, group.ExtraLabels)

	merged.SuppressedManifests = unionStringList(group.SuppressedManifests, node.SuppressedManifests)
	merged.TemplateRefs = unionTemplateRefs(group.TemplateRefs, node.TemplateRefs)
	merged.PruneManifests = unionResourceRefs(group.PruneManifests, node.PruneManifests)

	return merged
}

// GetGroup returns the group with the given name, if defined.
func (spec *ClusterInstanceSpec) GetGroup(name string) (*NodeGroupSpec, bool) {
	for i := range spec.Groups {
		if spec.Groups[i].Name == name {
			return &spec.Groups[i], true
		}
	}
	return nil, false
}

// NodeCount returns the number of nodes that reference this group.
func (group *NodeGroupSpec) NodeCount(nodes []NodeSpec) int {
	count := 0
	for _, node := range nodes {
		if node.Group == group.Name {
			count++
		}
	}
	return count
}

// EffectiveTemplateRefs returns the node's templateRefs unioned with its
// group's nodeSpec.templateRefs, group entries first, without duplicates.
func (n NodeSpec) EffectiveTemplateRefs(group *GroupNodeSpec) []TemplateRef {
	if group == nil {
		return n.TemplateRefs
	}
	return unionTemplateRefs(group.TemplateRefs, n.TemplateRefs)
}

// crdDefaultedScalars maps the shareable scalar NodeSpec fields that carry a
// +kubebuilder:default marker to their default values. The API server
// persists these defaults, so a Go struct value equal to the default is
// indistinguishable from an explicitly written one.
var crdDefaultedScalars = map[string]string{
	"role":                  "master",
	"bootMode":              "UEFI",
	"automatedCleaningMode": "disabled",
	"ironicInspect":         "",
}

// resolvesToDefault reports whether value equals the CRD default of field.
// Fields without a CRD default never resolve to a default.
func resolvesToDefault(field, value string) bool {
	def, ok := crdDefaultedScalars[field]
	return ok && value == def
}

// mergeScalar resolves a shared scalar field with nodes[] > group precedence.
// A node value that is unset, or that equals its CRD default while the group
// sets a different value, is treated as unspecified so the group value wins.
func mergeScalar(field, nodeVal, groupVal string) string {
	if groupVal == "" || groupVal == nodeVal {
		return nodeVal
	}
	if nodeVal == "" || resolvesToDefault(field, nodeVal) {
		return groupVal
	}
	return nodeVal
}

// mergeScalarCast resolves a shared scalar field whose Go type is a named
// string type.
func mergeScalarCast[T ~string](field, nodeVal, groupVal string) T {
	return T(mergeScalar(field, nodeVal, groupVal))
}

// unionStringMap unions two maps by key; the node value wins on collision.
// A nil result is returned when both inputs are nil.
func unionStringMap(nodeMap, groupMap map[string]string) map[string]string {
	if nodeMap == nil && groupMap == nil {
		return nil
	}
	merged := make(map[string]string, len(nodeMap)+len(groupMap))
	for k, v := range groupMap {
		merged[k] = v
	}
	for k, v := range nodeMap {
		merged[k] = v
	}
	return merged
}

// unionKindMap unions two per-Kind maps (e.g. extraLabels) by kind and then
// by key; the node value wins on collision. A nil result is returned when
// both inputs are nil.
func unionKindMap(nodeMap, groupMap map[string]map[string]string) map[string]map[string]string {
	if nodeMap == nil && groupMap == nil {
		return nil
	}
	merged := make(map[string]map[string]string, len(nodeMap)+len(groupMap))
	for kind, entries := range groupMap {
		merged[kind] = maps.Clone(entries)
	}
	for kind, entries := range nodeMap {
		if existing, ok := merged[kind]; ok && existing != nil {
			for k, v := range entries {
				existing[k] = v
			}
		} else {
			merged[kind] = maps.Clone(entries)
		}
	}
	return merged
}

// unionStringList unions two lists, group entries first, preserving order and
// removing duplicates. A nil result is returned when both inputs are nil.
func unionStringList(groupVals, nodeVals []string) []string {
	if groupVals == nil && nodeVals == nil {
		return nil
	}
	merged := make([]string, 0, len(groupVals)+len(nodeVals))
	seen := make(map[string]bool, len(groupVals)+len(nodeVals))
	for _, val := range groupVals {
		if !seen[val] {
			seen[val] = true
			merged = append(merged, val)
		}
	}
	for _, val := range nodeVals {
		if !seen[val] {
			seen[val] = true
			merged = append(merged, val)
		}
	}
	return merged
}

// unionTemplateRefs unions two template reference lists, group entries first,
// removing duplicates. A nil result is returned when both inputs are nil.
func unionTemplateRefs(groupRefs, nodeRefs []TemplateRef) []TemplateRef {
	if groupRefs == nil && nodeRefs == nil {
		return nil
	}
	merged := make([]TemplateRef, 0, len(groupRefs)+len(nodeRefs))
	seen := make(map[Reference]bool, len(groupRefs)+len(nodeRefs))
	for _, ref := range groupRefs {
		if !seen[Reference(ref)] {
			seen[Reference(ref)] = true
			merged = append(merged, ref)
		}
	}
	for _, ref := range nodeRefs {
		if !seen[Reference(ref)] {
			seen[Reference(ref)] = true
			merged = append(merged, ref)
		}
	}
	return merged
}

// unionResourceRefs unions two resource reference lists keyed by
// (apiVersion, kind), group entries first. A nil result is returned when
// both inputs are nil.
func unionResourceRefs(groupRefs, nodeRefs []ResourceRef) []ResourceRef {
	if groupRefs == nil && nodeRefs == nil {
		return nil
	}
	type resourceKey struct {
		apiVersion string
		kind       string
	}
	merged := make([]ResourceRef, 0, len(groupRefs)+len(nodeRefs))
	seen := make(map[resourceKey]bool, len(groupRefs)+len(nodeRefs))
	for _, ref := range groupRefs {
		key := resourceKey{apiVersion: ref.APIVersion, kind: ref.Kind}
		if !seen[key] {
			seen[key] = true
			merged = append(merged, ref)
		}
	}
	for _, ref := range nodeRefs {
		key := resourceKey{apiVersion: ref.APIVersion, kind: ref.Kind}
		if !seen[key] {
			seen[key] = true
			merged = append(merged, ref)
		}
	}
	return merged
}
