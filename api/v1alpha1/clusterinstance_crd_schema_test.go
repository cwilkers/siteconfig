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
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"
)

var _ = Describe("ClusterInstance CRD schema", func() {
	var crdPaths []string

	BeforeEach(func() {
		crdPaths = []string{
			filepath.Join("..", "..", "config", "crd", "bases",
				"siteconfig.open-cluster-management.io_clusterinstances.yaml"),
			filepath.Join("..", "..", "bundle", "manifests",
				"siteconfig.open-cluster-management.io_clusterinstances.yaml"),
		}
	})

	loadSpecProperties := func(crdPath string) map[string]interface{} {
		data, err := os.ReadFile(crdPath)
		Expect(err).ToNot(HaveOccurred())

		var crd map[string]interface{}
		Expect(yaml.Unmarshal(data, &crd)).To(Succeed())

		versions := crd["spec"].(map[string]interface{})["versions"].([]interface{})
		Expect(versions).ToNot(BeEmpty())

		version := versions[0].(map[string]interface{})
		schema := version["schema"].(map[string]interface{})["openAPIV3Schema"].(map[string]interface{})
		return schema["properties"].(map[string]interface{})["spec"].(map[string]interface{})["properties"].(map[string]interface{})
	}

	It("defines the nodes[].group field", func() {
		for _, crdPath := range crdPaths {
			nodeProps := loadSpecProperties(crdPath)["nodes"].(map[string]interface{})["items"].(map[string]interface{})["properties"].(map[string]interface{})
			Expect(nodeProps).To(HaveKey("group"))
		}
	})

	It("does not require nodes[].templateRefs so grouped nodes may inherit it", func() {
		for _, crdPath := range crdPaths {
			nodeItems := loadSpecProperties(crdPath)["nodes"].(map[string]interface{})["items"].(map[string]interface{})
			required, ok := nodeItems["required"].([]interface{})
			if ok {
				for _, field := range required {
					Expect(field).ToNot(Equal("templateRefs"),
						"nodes[].templateRefs must not be CRD-required; admission enforces effective templateRefs")
				}
			}
		}
	})

	It("defines the spec.groups array", func() {
		for _, crdPath := range crdPaths {
			Expect(loadSpecProperties(crdPath)).To(HaveKey("groups"))
		}
	})

	It("keys spec.groups by name so duplicates are rejected and order is not significant", func() {
		for _, crdPath := range crdPaths {
			groups := loadSpecProperties(crdPath)["groups"].(map[string]interface{})
			Expect(groups).To(HaveKeyWithValue("x-kubernetes-list-type", "map"))
			Expect(groups["x-kubernetes-list-map-keys"]).To(ConsistOf("name"))
		}
	})

	It("does not default any inheritable node field in the CRD", func() {
		// Inheritable fields are the ones shared by nodes[] and groups[].nodeSpec.
		// They are resolved post-merge by ApplyNodeSpecDefaults so an explicit value
		// at a more specific scope stays distinguishable from an omitted one. A CRD
		// default anywhere in one of these fields would be persisted by the API
		// server and break that, so walk each field's whole schema.
		var expectNoDefault func(schema interface{}, path string)
		expectNoDefault = func(schema interface{}, path string) {
			properties, ok := schema.(map[string]interface{})
			if !ok {
				return
			}

			Expect(properties).ToNot(HaveKey("default"), "%s must not be CRD-defaulted", path)

			if nested, ok := properties["properties"].(map[string]interface{}); ok {
				for name, child := range nested {
					expectNoDefault(child, path+"."+name)
				}
			}
			if items, ok := properties["items"]; ok {
				expectNoDefault(items, path+"[]")
			}
		}

		for _, crdPath := range crdPaths {
			specProps := loadSpecProperties(crdPath)

			nodeProps := specProps["nodes"].(map[string]interface{})["items"].(map[string]interface{})["properties"].(map[string]interface{})
			groupProps := specProps["groups"].(map[string]interface{})["items"].(map[string]interface{})["properties"].(map[string]interface{})
			groupNodeProps := groupProps["nodeSpec"].(map[string]interface{})["properties"].(map[string]interface{})

			Expect(groupNodeProps).ToNot(BeEmpty())
			for field := range groupNodeProps {
				Expect(nodeProps).To(HaveKey(field),
					"groups[].nodeSpec.%s must also exist on nodes[] to be inheritable", field)

				expectNoDefault(nodeProps[field], "nodes[]."+field)
				expectNoDefault(groupNodeProps[field], "groups[].nodeSpec."+field)
			}
		}
	})

	It("restricts groups[].nodeSpec to the shareable subset of NodeSpec", func() {
		expected := []string{
			"automatedCleaningMode",
			"bootMode",
			"cpuArchitecture",
			"extraAnnotations",
			"extraLabels",
			"ignitionConfigOverride",
			"installerArgs",
			"ironicInspect",
			"nodeLabels",
			"pruneManifests",
			"role",
			"rootDeviceHints",
			"suppressedManifests",
			"templateRefs",
		}

		// Identity fields must be omitted so the API server rejects them.
		forbidden := []string{
			"bmcAddress", "bmcCredentialsName", "bootMACAddress", "hostName", "hostRef", "nodeNetwork", "group",
		}

		for _, crdPath := range crdPaths {
			groupProps := loadSpecProperties(crdPath)["groups"].(map[string]interface{})["items"].(map[string]interface{})["properties"].(map[string]interface{})
			nodeSpecProps := groupProps["nodeSpec"].(map[string]interface{})["properties"].(map[string]interface{})

			Expect(nodeSpecProps).To(HaveLen(len(expected)))
			for _, field := range expected {
				Expect(nodeSpecProps).To(HaveKey(field))
			}
			for _, field := range forbidden {
				Expect(nodeSpecProps).ToNot(HaveKey(field))
			}
		}
	})
})
