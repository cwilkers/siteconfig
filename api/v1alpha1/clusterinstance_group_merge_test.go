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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

var _ = Describe("MergeNodeSpec", func() {
	var (
		group *GroupNodeSpec
		node  NodeSpec
	)

	BeforeEach(func() {
		group = &GroupNodeSpec{
			Role:                   "worker",
			CPUArchitecture:        "x86_64",
			BootMode:               "UEFI",
			AutomatedCleaningMode:  "disabled",
			IronicInspect:          "disabled",
			InstallerArgs:          `["--append-karg","a=1"]`,
			IgnitionConfigOverride: `{"ignition":{"version":"3.2.0"}}`,
			RootDeviceHints:        &bmh_v1alpha1.RootDeviceHints{DeviceName: "/dev/disk/by-path/group"},
			NodeLabels:             map[string]string{"group-label": "group", "shared": "group"},
			ExtraAnnotations: map[string]map[string]string{
				"BareMetalHost": {"group-annotation": "group", "shared": "group"},
			},
			ExtraLabels: map[string]map[string]string{
				"BareMetalHost": {"group-label": "group", "shared": "group"},
				"NMStateConfig": {"group-only": "group"},
			},
			SuppressedManifests: []string{"GroupOnlyKind", "SharedKind"},
			PruneManifests: []ResourceRef{
				{APIVersion: "v1", Kind: "GroupOnlyKind"},
				{APIVersion: "v1", Kind: "SharedKind"},
			},
			TemplateRefs: []TemplateRef{{Name: "group-template", Namespace: "ns"}},
		}

		node = NodeSpec{
			BmcAddress:         "idrac-virtualmedia://192.0.2.1/redfish/v1/Systems/System.Embedded.1",
			BmcCredentialsName: BmcCredentialsName{Name: "node-bmc-secret"},
			BootMACAddress:     "00:00:5E:00:53:00",
			HostName:           "node1",
			Group:              "workers",
			Role:               "worker",
			BootMode:           "UEFI",
			TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "ns"}},
		}
	})

	It("prefers node values over group values", func() {
		node.CPUArchitecture = "aarch64"
		node.BootMode = "UEFISecureBoot"
		node.AutomatedCleaningMode = "metadata"
		node.IronicInspect = "enabled"
		node.InstallerArgs = `["--append-karg","b=2"]`
		node.IgnitionConfigOverride = `{"ignition":{"version":"3.4.0"}}`
		node.RootDeviceHints = &bmh_v1alpha1.RootDeviceHints{DeviceName: "/dev/disk/by-path/node"}

		merged := MergeNodeSpec(group, node)
		Expect(string(merged.CPUArchitecture)).To(Equal("aarch64"))
		Expect(string(merged.BootMode)).To(Equal("UEFISecureBoot"))
		Expect(string(merged.AutomatedCleaningMode)).To(Equal("metadata"))
		Expect(string(merged.IronicInspect)).To(Equal("enabled"))
		Expect(merged.InstallerArgs).To(Equal(`["--append-karg","b=2"]`))
		Expect(merged.IgnitionConfigOverride).To(Equal(`{"ignition":{"version":"3.4.0"}}`))
		Expect(merged.RootDeviceHints.DeviceName).To(Equal("/dev/disk/by-path/node"))
	})

	It("treats a defaulted node role as unspecified when the group sets a role", func() {
		node.Role = "master" // equals the CRD default
		merged := MergeNodeSpec(group, node)
		Expect(merged.Role).To(Equal("worker"))
	})

	It("treats a defaulted node bootMode as unspecified when the group sets a bootMode", func() {
		node.BootMode = "UEFI" // equals the CRD default
		group.BootMode = "UEFISecureBoot"
		merged := MergeNodeSpec(group, node)
		Expect(string(merged.BootMode)).To(Equal("UEFISecureBoot"))
	})

	It("keeps an explicitly defaulted group role when the node is defaulted", func() {
		node.Role = "master"
		group.Role = "master"
		merged := MergeNodeSpec(group, node)
		Expect(merged.Role).To(Equal("master"))
	})

	It("uses the group value when the node scalar is unset", func() {
		node.Role = ""
		node.BootMode = ""
		node.AutomatedCleaningMode = ""
		node.IronicInspect = ""
		merged := MergeNodeSpec(group, node)
		Expect(merged.Role).To(Equal("worker"))
		Expect(string(merged.BootMode)).To(Equal("UEFI"))
		Expect(string(merged.AutomatedCleaningMode)).To(Equal("disabled"))
		Expect(string(merged.IronicInspect)).To(Equal("disabled"))
		Expect(merged.InstallerArgs).To(Equal(`["--append-karg","a=1"]`))
		Expect(merged.RootDeviceHints).To(Equal(group.RootDeviceHints))
	})

	It("unions templateRefs without duplicates, group first", func() {
		node.TemplateRefs = []TemplateRef{
			{Name: "node-template", Namespace: "ns"},
			{Name: "group-template", Namespace: "ns"}, // duplicate of group entry
			{Name: "other-node-template", Namespace: "ns"},
		}
		merged := MergeNodeSpec(group, node)
		Expect(merged.TemplateRefs).To(Equal([]TemplateRef{
			{Name: "group-template", Namespace: "ns"},
			{Name: "node-template", Namespace: "ns"},
			{Name: "other-node-template", Namespace: "ns"},
		}))
	})

	It("unions nodeLabels with node winning on key collision", func() {
		node.NodeLabels = map[string]string{"node-label": "node", "shared": "node"}
		merged := MergeNodeSpec(group, node)
		Expect(merged.NodeLabels).To(Equal(map[string]string{
			"group-label": "group",
			"node-label":  "node",
			"shared":      "node",
		}))
	})

	It("unions extraLabels by kind then key with node winning", func() {
		node.ExtraLabels = map[string]map[string]string{
			"BareMetalHost": {"node-label": "node", "shared": "node"},
			"NodePool":      {"node-only": "node"},
		}
		merged := MergeNodeSpec(group, node)
		Expect(merged.ExtraLabels).To(Equal(map[string]map[string]string{
			"BareMetalHost": {"group-label": "group", "node-label": "node", "shared": "node"},
			"NMStateConfig": {"group-only": "group"},
			"NodePool":      {"node-only": "node"},
		}))
	})

	It("unions extraAnnotations by kind then key with node winning", func() {
		node.ExtraAnnotations = map[string]map[string]string{
			"BareMetalHost": {"node-annotation": "node", "shared": "node"},
		}
		merged := MergeNodeSpec(group, node)
		Expect(merged.ExtraAnnotations).To(Equal(map[string]map[string]string{
			"BareMetalHost": {"group-annotation": "group", "node-annotation": "node", "shared": "node"},
		}))
	})

	It("unions suppressedManifests without duplicates", func() {
		node.SuppressedManifests = []string{"NodeOnlyKind", "SharedKind"}
		merged := MergeNodeSpec(group, node)
		Expect(merged.SuppressedManifests).To(Equal([]string{"GroupOnlyKind", "SharedKind", "NodeOnlyKind"}))
	})

	It("unions pruneManifests by apiVersion+kind", func() {
		node.PruneManifests = []ResourceRef{
			{APIVersion: "v1", Kind: "SharedKind"}, // duplicate by identity
			{APIVersion: "v1", Kind: "NodeOnlyKind"},
			{APIVersion: "v2", Kind: "SharedKind"}, // different apiVersion is distinct
		}
		merged := MergeNodeSpec(group, node)
		Expect(merged.PruneManifests).To(ConsistOf(
			ResourceRef{APIVersion: "v1", Kind: "GroupOnlyKind"},
			ResourceRef{APIVersion: "v1", Kind: "SharedKind"},
			ResourceRef{APIVersion: "v1", Kind: "NodeOnlyKind"},
			ResourceRef{APIVersion: "v2", Kind: "SharedKind"},
		))
	})

	It("copies identity fields only from the node", func() {
		merged := MergeNodeSpec(group, node)
		Expect(merged.BmcAddress).To(Equal(node.BmcAddress))
		Expect(merged.BmcCredentialsName).To(Equal(node.BmcCredentialsName))
		Expect(merged.BootMACAddress).To(Equal(node.BootMACAddress))
		Expect(merged.HostName).To(Equal(node.HostName))
		Expect(merged.HostRef).To(Equal(node.HostRef))
		Expect(merged.NodeNetwork).To(Equal(node.NodeNetwork))
		Expect(merged.Group).To(Equal(node.Group))
	})

	It("returns the node unchanged when the group is nil", func() {
		node.NodeLabels = map[string]string{"a": "b"}
		merged := MergeNodeSpec(nil, node)
		Expect(merged).To(Equal(node))
	})

	It("is idempotent", func() {
		node.NodeLabels = map[string]string{"node-label": "node", "shared": "node"}
		node.SuppressedManifests = []string{"NodeOnlyKind", "SharedKind"}
		merged := MergeNodeSpec(group, node)
		Expect(MergeNodeSpec(group, merged)).To(Equal(merged))
	})
})

var _ = Describe("group spec helpers", func() {
	var spec *ClusterInstanceSpec

	BeforeEach(func() {
		spec = &ClusterInstanceSpec{
			Groups: []NodeGroupSpec{
				{Name: "workers"},
				{Name: "storage"},
			},
			Nodes: []NodeSpec{
				{HostName: "node1", Group: "workers"},
				{HostName: "node2", Group: "workers"},
				{HostName: "node3", Group: "storage"},
				{HostName: "node4"},
			},
		}
	})

	It("GetGroup returns the group by name", func() {
		group, ok := spec.GetGroup("storage")
		Expect(ok).To(BeTrue())
		Expect(group.Name).To(Equal("storage"))
	})

	It("GetGroup reports not found for an unknown name", func() {
		_, ok := spec.GetGroup("missing")
		Expect(ok).To(BeFalse())
	})

	It("NodeCount counts only member nodes", func() {
		Expect(spec.Groups[0].NodeCount(spec.Nodes)).To(Equal(2))
		Expect(spec.Groups[1].NodeCount(spec.Nodes)).To(Equal(1))
	})

	It("EffectiveTemplateRefs returns the node refs without a group", func() {
		node := NodeSpec{TemplateRefs: []TemplateRef{{Name: "node-template", Namespace: "ns"}}}
		Expect(node.EffectiveTemplateRefs(nil)).To(Equal(node.TemplateRefs))
	})

	It("EffectiveTemplateRefs unions group refs first", func() {
		node := NodeSpec{
			TemplateRefs: []TemplateRef{{Name: "node-template", Namespace: "ns"}},
		}
		group := &GroupNodeSpec{
			TemplateRefs: []TemplateRef{
				{Name: "group-template", Namespace: "ns"},
				{Name: "node-template", Namespace: "ns"},
			},
		}
		Expect(node.EffectiveTemplateRefs(group)).To(Equal([]TemplateRef{
			{Name: "group-template", Namespace: "ns"},
			{Name: "node-template", Namespace: "ns"},
		}))
	})
})

var _ = Describe("resolvesToDefault", func() {
	It("recognizes the CRD defaults for the defaulted scalar fields", func() {
		Expect(resolvesToDefault("role", "master")).To(BeTrue())
		Expect(resolvesToDefault("bootMode", "UEFI")).To(BeTrue())
		Expect(resolvesToDefault("automatedCleaningMode", "disabled")).To(BeTrue())
		Expect(resolvesToDefault("ironicInspect", "")).To(BeTrue())
	})

	It("does not treat non-default values as defaulted", func() {
		Expect(resolvesToDefault("role", "worker")).To(BeFalse())
		Expect(resolvesToDefault("bootMode", "UEFISecureBoot")).To(BeFalse())
		Expect(resolvesToDefault("automatedCleaningMode", "metadata")).To(BeFalse())
		Expect(resolvesToDefault("ironicInspect", "enabled")).To(BeFalse())
	})

	It("does not treat fields without CRD defaults as defaulted", func() {
		Expect(resolvesToDefault("cpuArchitecture", "x86_64")).To(BeFalse())
		Expect(resolvesToDefault("installerArgs", "")).To(BeFalse())
	})
})

var _ = Describe("unionKindMap nil handling", func() {
	It("does not panic when the group maps a kind to a nil inner map", func() {
		group := &GroupNodeSpec{
			ExtraLabels: map[string]map[string]string{"BareMetalHost": nil},
		}
		node := NodeSpec{
			ExtraLabels: map[string]map[string]string{"BareMetalHost": {"node-label": "node"}},
		}
		merged := MergeNodeSpec(group, node)
		Expect(merged.ExtraLabels).To(Equal(map[string]map[string]string{
			"BareMetalHost": {"node-label": "node"},
		}))
	})
})
