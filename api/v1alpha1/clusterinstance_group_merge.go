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
// prefer the node value, nodeLabels is unioned by key with the node value
// winning on collision, extraLabels and extraAnnotations resolve per Kind with
// the node's map replacing the group's for the same Kind, and lists are
// unioned with group entries first. Identity fields (hostName, bmcAddress,
// bootMACAddress, bmcCredentialsName, hostRef, nodeNetwork, group) are always
// taken from the node.
//
// MergeNodeSpec does not apply defaults: an unset field stays unset so that
// an explicit value at a more specific scope is distinguishable from an
// omitted one. Use EffectiveNodeSpec to obtain the fully resolved spec used
// for rendering and validation.
func MergeNodeSpec(group *GroupNodeSpec, node NodeSpec) NodeSpec {
	if group == nil {
		return node
	}

	merged := node

	merged.Role = mergeScalar(node.Role, group.Role)
	merged.CPUArchitecture = mergeScalarCast[CPUArchitecture](
		string(node.CPUArchitecture), string(group.CPUArchitecture))
	merged.BootMode = mergeScalarCast[bmh_v1alpha1.BootMode](string(node.BootMode), string(group.BootMode))
	merged.AutomatedCleaningMode = mergeScalarCast[bmh_v1alpha1.AutomatedCleaningMode](
		string(node.AutomatedCleaningMode), string(group.AutomatedCleaningMode))
	merged.IronicInspect = mergeScalarCast[IronicInspect](string(node.IronicInspect), string(group.IronicInspect))
	merged.InstallerArgs = mergeScalar(node.InstallerArgs, group.InstallerArgs)
	merged.IgnitionConfigOverride = mergeScalar(node.IgnitionConfigOverride, group.IgnitionConfigOverride)

	if node.RootDeviceHints == nil {
		// Deep copy so the effective spec does not alias the group's hints;
		// every other field of the merge owns its data.
		merged.RootDeviceHints = group.RootDeviceHints.DeepCopy()
	}

	merged.NodeLabels = unionStringMap(node.NodeLabels, group.NodeLabels)
	merged.ExtraAnnotations = overrideKindMap(node.ExtraAnnotations, group.ExtraAnnotations)
	merged.ExtraLabels = overrideKindMap(node.ExtraLabels, group.ExtraLabels)

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

// ApplyNodeSpecDefaults sets the rendering defaults for NodeSpec fields that
// remain unset after group merging.
func ApplyNodeSpecDefaults(node NodeSpec) NodeSpec {
	if node.Role == "" {
		node.Role = "master"
	}
	if node.BootMode == "" {
		node.BootMode = bmh_v1alpha1.UEFI
	}
	if node.AutomatedCleaningMode == "" {
		node.AutomatedCleaningMode = bmh_v1alpha1.CleaningModeDisabled
	}
	return node
}

// EffectiveNodeSpec returns the node spec after group merging and applying the
// rendering defaults. Use it wherever the resolved configuration is consumed,
// such as template rendering and validation.
func EffectiveNodeSpec(group *GroupNodeSpec, node NodeSpec) NodeSpec {
	return ApplyNodeSpecDefaults(MergeNodeSpec(group, node))
}

// effectiveNodeSpec resolves the node's group from a prebuilt index and returns
// its effective (merged and defaulted) spec. An unknown group is treated as
// absent; validation rejects unknown group references separately.
func effectiveNodeSpec(node NodeSpec, groupIndex map[string]*NodeGroupSpec) NodeSpec {
	var groupSpec *GroupNodeSpec
	if group, exists := groupIndex[node.Group]; exists {
		groupSpec = &group.NodeSpec
	}
	return EffectiveNodeSpec(groupSpec, node)
}

// mergeScalar resolves a shared scalar field with nodes[] > group precedence.
// A field set on the node wins; an unset node field falls back to the group.
func mergeScalar(nodeVal, groupVal string) string {
	if nodeVal != "" {
		return nodeVal
	}
	return groupVal
}

// mergeScalarCast resolves a shared scalar field whose Go type is a named
// string type.
func mergeScalarCast[T ~string](nodeVal, groupVal string) T {
	return T(mergeScalar(nodeVal, groupVal))
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

// overrideKindMap resolves two per-Kind maps (e.g. extraLabels) by Kind: a Kind
// the node defines replaces the group's map for that Kind entirely, matching
// how a Kind at a more specific scope replaces the cluster-level map. Kinds the
// node does not define are taken from the group. A nil result is returned when
// both inputs are nil.
func overrideKindMap(nodeMap, groupMap map[string]map[string]string) map[string]map[string]string {
	if nodeMap == nil && groupMap == nil {
		return nil
	}
	merged := make(map[string]map[string]string, len(nodeMap)+len(groupMap))
	for kind, entries := range groupMap {
		merged[kind] = maps.Clone(entries)
	}
	for kind, entries := range nodeMap {
		merged[kind] = maps.Clone(entries)
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
