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

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("HostedClusterReconcile", func() {
	var (
		c                client.Client
		r                *HostedClusterReconciler
		ctx              = context.Background()
		testLogger       = zap.NewNop().Named("Test")
		clusterName      = "test-hc-cluster"
		clusterNamespace = "test-hc-namespace"
		clusterInstance  *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		// Register the HostedCluster scheme
		Expect(hypershiftv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}, &hypershiftv1beta1.HostedCluster{}).
			Build()
		r = &HostedClusterReconciler{
			Client: c,
			Scheme: scheme.Scheme,
			Log:    testLogger,
		}

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterName,
				Namespace:  clusterNamespace,
				Finalizers: []string{clusterInstanceFinalizer},
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName:            clusterName,
				PullSecretRef:          corev1.LocalObjectReference{Name: "pull-secret"},
				ClusterImageSetNameRef: "testimage:foobar",
				SSHPublicKey:           "test-ssh",
				BaseDomain:             "abcd",
				ClusterType:            v1alpha1.ClusterTypeHostedControlPlane,
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: "test-cluster-template", Namespace: "default"}},
				Nodes: []v1alpha1.NodeSpec{{
					BmcAddress:         "192.0.2.0",
					BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: "bmc"},
					TemplateRefs: []v1alpha1.TemplateRef{
						{Name: "test-node-template", Namespace: "default"}}}}},
		}

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterNamespace,
			},
		}
		Expect(c.Create(ctx, ns)).To(Succeed())
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
	})

	It("doesn't error for a missing HostedCluster", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
	})

	It("doesn't reconcile a HostedCluster that is not owned by ClusterInstance", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}
		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, "not-the-owner"),
				},
			},
		}
		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		// Fetch ClusterInstance and verify that the status is unchanged
		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())
		Expect(ci.Status).To(Equal(clusterInstance.Status))
	})

	It("tests that HostedClusterReconciler initializes ClusterInstance Provisioned condition", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}
		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, clusterName),
				},
			},
		}
		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionUnknown,
			Reason: string(v1alpha1.Unknown),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
	})

	It("tests that ClusterInstance provisioned status is set to True when history[0].state is Completed", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		completionTime := metav1.NewTime(time.Now())
		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, clusterName),
				},
			},
			Status: hypershiftv1beta1.HostedClusterStatus{
				Version: &hypershiftv1beta1.ClusterVersionStatus{
					History: []configv1.UpdateHistory{
						{
							State:          configv1.CompletedUpdate,
							StartedTime:    metav1.NewTime(time.Now().Add(-1 * time.Hour)),
							CompletionTime: &completionTime,
							Version:        "4.14.0",
							Image:          "quay.io/openshift-release-dev/ocp-release:4.14.0",
							Verified:       true,
						},
					},
				},
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionTrue,
			Reason: string(v1alpha1.Completed),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
		Expect(found.Message).To(ContainSubstring("4.14.0"))
	})

	It("tests that ClusterInstance provisioned status is set to InProgress when history[0].state is Partial", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, clusterName),
				},
			},
			Status: hypershiftv1beta1.HostedClusterStatus{
				Version: &hypershiftv1beta1.ClusterVersionStatus{
					History: []configv1.UpdateHistory{
						{
							State:       configv1.PartialUpdate,
							StartedTime: metav1.NewTime(time.Now().Add(-30 * time.Minute)),
							Version:     "4.14.0",
							Image:       "quay.io/openshift-release-dev/ocp-release:4.14.0",
							Verified:    true,
						},
					},
				},
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionFalse,
			Reason: string(v1alpha1.InProgress),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
		Expect(found.Message).To(ContainSubstring("4.14.0"))
	})

	It("tests that ClusterInstance provisioned status remains Unknown when history is empty", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, clusterName),
				},
			},
			Status: hypershiftv1beta1.HostedClusterStatus{
				Version: &hypershiftv1beta1.ClusterVersionStatus{
					History: []configv1.UpdateHistory{},
				},
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionUnknown,
			Reason: string(v1alpha1.Unknown),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
	})

	It("tests that ClusterInstance provisioned status remains Unknown when version status is nil", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, clusterName),
				},
			},
			Status: hypershiftv1beta1.HostedClusterStatus{
				Version: nil,
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionUnknown,
			Reason: string(v1alpha1.Unknown),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
	})

	It("tests that multiple history entries use only the first one", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		completionTime := metav1.NewTime(time.Now())
		oldCompletionTime := metav1.NewTime(time.Now().Add(-24 * time.Hour))

		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, clusterName),
				},
			},
			Status: hypershiftv1beta1.HostedClusterStatus{
				Version: &hypershiftv1beta1.ClusterVersionStatus{
					History: []configv1.UpdateHistory{
						{
							State:          configv1.CompletedUpdate,
							StartedTime:    metav1.NewTime(time.Now().Add(-1 * time.Hour)),
							CompletionTime: &completionTime,
							Version:        "4.15.0",
							Image:          "quay.io/openshift-release-dev/ocp-release:4.15.0",
							Verified:       true,
						},
						{
							State:          configv1.CompletedUpdate,
							StartedTime:    metav1.NewTime(time.Now().Add(-25 * time.Hour)),
							CompletionTime: &oldCompletionTime,
							Version:        "4.14.0",
							Image:          "quay.io/openshift-release-dev/ocp-release:4.14.0",
							Verified:       true,
						},
					},
				},
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionTrue,
			Reason: string(v1alpha1.Completed),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
		// Should reference the first (latest) version, not the old one
		Expect(found.Message).To(ContainSubstring("4.15.0"))
		Expect(found.Message).NotTo(ContainSubstring("4.14.0"))
	})
})
