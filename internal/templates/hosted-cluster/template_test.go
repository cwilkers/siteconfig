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

package hostedcluster

import (
	"bytes"
	"testing"

	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHostedClusterTemplates(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Hosted Cluster Installation Templates")
}

var _ = BeforeSuite(func() {
	Expect(v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
})

var _ = Describe("Get Installation Templates", func() {

	It("should return the expected Cluster-level installation templates", func() {
		clusterTemplates := GetClusterTemplates()
		expectedCRs := map[string]string{
			"HostedCluster":         HostedCluster,
			"ManagedCluster":        ManagedCluster,
			"KlusterletAddonConfig": KlusterletAddonConfig,
			"SshPubKeySecret":       SshPubKeySecret,
		}
		Expect(clusterTemplates).To(Equal(expectedCRs))
	})

	It("should return the expected Nodel-level installation templates", func() {
		nodeTemplates := GetNodeTemplates()
		expectedCRs := map[string]string{
			"InfraEnv":      InfraEnv,
			"NodePool":      NodePool,
			"BareMetalHost": BareMetalHost,
			"NMStateConfig": NMStateConfig,
		}
		Expect(nodeTemplates).To(Equal(expectedCRs))
	})
})

var _ = Describe("Validate Installation Templates", func() {

	It("should be able to parse the Cluster-level installation templates", func() {
		clusterTemplates := GetClusterTemplates()
		for templateKey, template := range clusterTemplates {
			_, err := ci.ParseTemplate(templateKey, template)
			Expect(err).ToNot(HaveOccurred())
		}
	})

	It("should be able to parse the Node-level installation templates", func() {
		nodeTemplates := GetNodeTemplates()
		for templateKey, template := range nodeTemplates {
			_, err := ci.ParseTemplate(templateKey, template)
			Expect(err).ToNot(HaveOccurred())
		}
	})
})

var _ = Describe("Group installation templates", func() {
	const (
		clusterName  = "site-sno-du-1"
		groupName    = "workers"
		pausedUntil  = "Fri, 31 Dec 9999 23:59:59 GMT"
		nmstateLabel = clusterName + "-" + groupName
		nodeHostName = "worker-1"
	)

	newGroupData := func() *ci.ClusterData {
		return &ci.ClusterData{
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName:  clusterName,
				SSHPublicKey: "ssh-rsa AAAAB3NzaC1yc2E",
				PullSecretRef: corev1.LocalObjectReference{
					Name: "pull-secret",
				},
				AdditionalNTPSources: []string{"ntp.example.com"},
				CPUArchitecture:      v1alpha1.CPUArchitectureX86_64,
			},
			SpecialVars: ci.SpecialVars{
				GroupName:    groupName,
				NodeCount:    3,
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64",
			},
		}
	}

	newGroupedNodeData := func() *ci.ClusterData {
		data := newGroupData()
		data.SpecialVars.CurrentNode = v1alpha1.NodeSpec{
			HostName:              nodeHostName,
			Role:                  "worker",
			BmcAddress:            "idrac-virtualmedia://192.168.1.10/redfish/v1/Systems/System.Embedded.1",
			BmcCredentialsName:    v1alpha1.BmcCredentialsName{Name: "worker-1-bmc"},
			BootMACAddress:        "AA:BB:CC:DD:EE:01",
			BootMode:              bmh_v1alpha1.UEFI,
			AutomatedCleaningMode: bmh_v1alpha1.CleaningModeDisabled,
			NodeNetwork: &aiv1beta1.NMStateConfigSpec{
				NetConfig: aiv1beta1.NetConfig{Raw: []byte("interfaces:\n- name: eno1\n")},
			},
		}
		return data
	}

	It("returns the expected group and grouped-node template sets", func() {
		Expect(GetGroupTemplates()).To(Equal(map[string]string{
			"GroupInfraEnv": GroupInfraEnv,
			"GroupNodePool": GroupNodePool,
		}))
		Expect(GetGroupedNodeTemplates()).To(Equal(map[string]string{
			"GroupedNodeBareMetalHost": GroupedNodeBareMetalHost,
			"GroupedNodeNMStateConfig": GroupedNodeNMStateConfig,
		}))
	})

	It("parses every group and grouped-node template", func() {
		for templateKey, template := range GetGroupTemplates() {
			_, err := ci.ParseTemplate(templateKey, template)
			Expect(err).ToNot(HaveOccurred())
		}
		for templateKey, template := range GetGroupedNodeTemplates() {
			_, err := ci.ParseTemplate(templateKey, template)
			Expect(err).ToNot(HaveOccurred())
		}
	})

	It("renders a NodePool with replicas equal to the group node count", func() {
		obj := renderGroupTemplate(GroupNodePool, newGroupData())

		Expect(obj).To(HaveKeyWithValue("kind", "NodePool"))
		name, _, err := unstructured.NestedString(obj, "metadata", "name")
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal(groupName))

		replicas, found, err := unstructured.NestedFieldNoCopy(obj, "spec", "replicas")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(replicas).To(BeNumerically("==", 3))

		selector, found, err := unstructured.NestedStringMap(
			obj, "spec", "platform", "agent", "agentLabelSelector", "matchLabels")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(selector).To(HaveKeyWithValue(GroupLabelKey, groupName))
	})

	It("omits pausedUntil when the group is not paused", func() {
		obj := renderGroupTemplate(GroupNodePool, newGroupData())

		_, found, err := unstructured.NestedString(obj, "spec", "pausedUntil")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeFalse())
	})

	It("renders pausedUntil when the group is paused", func() {
		data := newGroupData()
		data.SpecialVars.GroupPaused = true

		obj := renderGroupTemplate(GroupNodePool, data)

		value, found, err := unstructured.NestedString(obj, "spec", "pausedUntil")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(value).To(Equal(pausedUntil))
	})

	It("omits config and tuningConfig when the refs are empty", func() {
		obj := renderGroupTemplate(GroupNodePool, newGroupData())

		for _, field := range []string{"config", "tuningConfig"} {
			_, found, err := unstructured.NestedSlice(obj, "spec", field)
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeFalse(), "spec.%s must be omitted when empty", field)
		}
	})

	It("renders config and tuningConfig from the group refs", func() {
		data := newGroupData()
		data.SpecialVars.MachineConfigRefs = []string{"worker-machineconfig"}
		data.SpecialVars.TuningConfigRefs = []string{"worker-performanceprofile"}

		obj := renderGroupTemplate(GroupNodePool, data)

		config, found, err := unstructured.NestedSlice(obj, "spec", "config")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(config).To(Equal([]interface{}{
			map[string]interface{}{"name": "worker-machineconfig"},
		}))

		tuningConfig, found, err := unstructured.NestedSlice(obj, "spec", "tuningConfig")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(tuningConfig).To(Equal([]interface{}{
			map[string]interface{}{"name": "worker-performanceprofile"},
		}))
	})

	It("labels group InfraEnv agents with the group label", func() {
		obj := renderGroupTemplate(GroupInfraEnv, newGroupData())

		name, _, err := unstructured.NestedString(obj, "metadata", "name")
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal(groupName))

		agentLabels, found, err := unstructured.NestedStringMap(obj, "spec", "agentLabels")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(agentLabels).To(HaveKeyWithValue(GroupLabelKey, groupName))

		selector, found, err := unstructured.NestedStringMap(
			obj, "spec", "nmStateConfigLabelSelector", "matchLabels")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(selector).To(HaveKeyWithValue("nmstate-label", nmstateLabel))
	})

	It("binds grouped nodes to the group InfraEnv", func() {
		obj := renderGroupTemplate(GroupedNodeBareMetalHost, newGroupedNodeData())

		name, _, err := unstructured.NestedString(obj, "metadata", "name")
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal(nodeHostName))

		labels, found, err := unstructured.NestedStringMap(obj, "metadata", "labels")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(labels).To(HaveKeyWithValue("infraenvs.agent-install.openshift.io", groupName))
		Expect(labels).To(HaveKeyWithValue(GroupLabelKey, groupName))
	})

	It("labels grouped NMStateConfigs with a cluster-and-group scoped label", func() {
		obj := renderGroupTemplate(GroupedNodeNMStateConfig, newGroupedNodeData())

		name, _, err := unstructured.NestedString(obj, "metadata", "name")
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal(nodeHostName))

		labels, found, err := unstructured.NestedStringMap(obj, "metadata", "labels")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(labels).To(HaveKeyWithValue("nmstate-label", nmstateLabel))
	})
})

func renderGroupTemplate(templateStr string, data *ci.ClusterData) map[string]interface{} {
	t, err := ci.ParseTemplate("group-template", templateStr)
	Expect(err).ToNot(HaveOccurred())

	var buffer bytes.Buffer
	Expect(t.Execute(&buffer, data)).To(Succeed())
	expectNoTemplateArtifacts(buffer.String())

	var obj map[string]interface{}
	Expect(sigsyaml.Unmarshal(buffer.Bytes(), &obj)).To(Succeed())
	return obj
}

func expectNoTemplateArtifacts(rendered string) {
	Expect(rendered).ToNot(ContainSubstring("<no value>"))
	Expect(rendered).ToNot(ContainSubstring("<nil>"))
}
