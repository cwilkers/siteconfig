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

package assistedinstaller

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

func TestAssistedInstallerTemplates(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Assisted Installer Installation Templates")
}

var _ = BeforeSuite(func() {
	Expect(v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
})

var _ = Describe("Get Installation Templates", func() {

	It("should return the expected Cluster-level installation templates", func() {
		clusterTemplates := GetClusterTemplates()
		expectedCRs := map[string]string{
			"AgentClusterInstall":   AgentClusterInstall,
			"ClusterDeployment":     ClusterDeployment,
			"ManagedCluster":        ManagedCluster,
			"KlusterletAddonConfig": KlusterletAddonConfig,
		}
		Expect(clusterTemplates).To(Equal(expectedCRs))
	})

	It("should return the expected Nodel-level installation templates", func() {
		nodeTemplates := GetNodeTemplates()
		expectedCRs := map[string]string{
			"InfraEnv":      InfraEnv,
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
		}))
		Expect(GetGroupedNodeTemplates()).To(Equal(map[string]string{
			"GroupedNodeBareMetalHost": GroupedNodeBareMetalHost,
			"GroupedNodeNMStateConfig": GroupedNodeNMStateConfig,
		}))
	})

	It("does not ship a group NodePool template", func() {
		Expect(GetGroupTemplates()).ToNot(HaveKey("GroupNodePool"))
		Expect(GetGroupedNodeTemplates()).ToNot(HaveKey("GroupNodePool"))
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

	It("labels group InfraEnv agents with the group label and refers to the cluster", func() {
		obj := renderGroupTemplate(GroupInfraEnv, newGroupData())

		name, _, err := unstructured.NestedString(obj, "metadata", "name")
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal(groupName))

		clusterRef, found, err := unstructured.NestedStringMap(obj, "spec", "clusterRef")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(clusterRef).To(HaveKeyWithValue("name", clusterName))

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
		Expect(labels).To(HaveKeyWithValue("cluster.open-cluster-management.io/backup", "cluster-activation"))
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
