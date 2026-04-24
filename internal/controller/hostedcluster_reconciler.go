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
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// HostedClusterReconciler reconciles a HostedCluster object to
// update the ClusterInstance cluster provisioned status conditions
type HostedClusterReconciler struct {
	client.Client
	Log    *zap.Logger
	Scheme *runtime.Scheme
}

func (r *HostedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	log := r.Log.With(
		zap.String("name", req.Name),
		zap.String("namespace", req.Namespace),
	)

	// Get the HostedCluster CR
	hostedCluster := &hypershiftv1beta1.HostedCluster{}
	if err := r.Get(ctx, req.NamespacedName, hostedCluster); err != nil {
		if errors.IsNotFound(err) {
			log.Info("HostedCluster not found")
			return doNotRequeue(), nil
		}
		log.Error("Failed to get HostedCluster", zap.Error(err))
		// This is likely a case where the API is down, so requeue and try again shortly
		return requeueWithError(err)
	}

	// Fetch ClusterInstance associated with HostedCluster object
	clusterInstance, err := r.getClusterInstance(ctx, log, hostedCluster)
	if clusterInstance == nil {
		return doNotRequeue(), nil
	} else if err != nil {
		return requeueWithError(err)
	}

	patch := client.MergeFrom(clusterInstance.DeepCopy())

	// Initialize ClusterInstance Provisioned status if not found
	if provisionedStatus := meta.FindStatusCondition(
		clusterInstance.Status.Conditions,
		string(v1alpha1.ClusterProvisioned),
	); provisionedStatus == nil {
		log.Info("Initializing Provisioned condition", zap.String("ClusterInstance", clusterInstance.Name))
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.Unknown,
			metav1.ConditionUnknown,
			"Waiting for provisioning to start")
	}

	updateCIProvisionedStatusFromHostedCluster(hostedCluster, clusterInstance, log)
	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		return requeueWithError(updateErr)
	}

	return doNotRequeue(), nil
}

// updateCIProvisionedStatusFromHostedCluster updates the ClusterInstance Provisioned condition
// based on HostedCluster.status.version.history[0] state.
//
// The history array is ordered by recency with the newest update first (index 0).
// We examine the first entry to determine the current provisioning state:
// - State=Completed -> Provisioned=True (cluster successfully deployed)
// - State=Partial -> Provisioned=InProgress (cluster deployment in progress)
// - No history or nil version -> Provisioned=Unknown (waiting for deployment to start)
func updateCIProvisionedStatusFromHostedCluster(
	hc *hypershiftv1beta1.HostedCluster,
	ci *v1alpha1.ClusterInstance,
	log *zap.Logger,
) {
	// Check if version status exists
	if hc.Status.Version == nil {
		log.Debug("HostedCluster.Status.Version is nil")
		return
	}

	// Check if history is empty
	if len(hc.Status.Version.History) == 0 {
		log.Debug("HostedCluster.Status.Version.History is empty")
		return
	}

	// Get the first (most recent) history entry
	latestHistory := hc.Status.Version.History[0]

	// Build a descriptive message including version and image
	message := fmt.Sprintf("Cluster version %s", latestHistory.Version)
	if latestHistory.Image != "" {
		message = fmt.Sprintf("%s (image: %s)", message, latestHistory.Image)
	}

	switch latestHistory.State {
	case configv1.CompletedUpdate:
		// Installation completed successfully
		conditions.SetStatusCondition(&ci.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.Completed,
			metav1.ConditionTrue,
			message)

	case configv1.PartialUpdate:
		// Installation in progress or partially applied
		message = fmt.Sprintf("Provisioning %s", message)
		conditions.SetStatusCondition(&ci.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.InProgress,
			metav1.ConditionFalse,
			message)

	default:
		// Unknown state
		log.Info("Unknown update state", zap.String("state", string(latestHistory.State)))
		conditions.SetStatusCondition(&ci.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.Unknown,
			metav1.ConditionUnknown,
			fmt.Sprintf("Unknown state: %s", latestHistory.State))
	}
}

func (r *HostedClusterReconciler) getClusterInstance(
	ctx context.Context,
	log *zap.Logger,
	hc *hypershiftv1beta1.HostedCluster,
) (*v1alpha1.ClusterInstance, error) {
	ownedBy := getClusterInstanceOwner(hc.Labels)
	if ownedBy == "" {
		log.Info("ClusterInstance owner reference not found for HostedCluster")
		return nil, nil
	}

	clusterInstanceRef, err := ci.GetNamespacedNameFromOwnedByLabel(ownedBy)
	if err != nil {
		return nil, fmt.Errorf("failed to get namespaced name from OwnedBy label (%s): %w", ownedBy, err)
	}

	if clusterInstanceRef.Namespace != hc.Namespace {
		return nil, fmt.Errorf("hostedCluster namespace [%s] does not match ClusterInstance namespace [%s]",
			hc.Namespace, clusterInstanceRef.Namespace)
	}

	clusterInstance := &v1alpha1.ClusterInstance{}
	if err := r.Get(ctx, clusterInstanceRef,
		clusterInstance); err != nil {
		if errors.IsNotFound(err) {
			log.Info("ClusterInstance not found", zap.String("name", clusterInstanceRef.String()))
			return nil, nil
		}
		log.Info("Failed to get ClusterInstance", zap.String("name", clusterInstanceRef.String()))
		return nil, fmt.Errorf("failed to get ClusterInstance %s: %w", clusterInstanceRef.String(), err)
	}
	return clusterInstance, nil
}

func (r *HostedClusterReconciler) mapClusterInstanceToHostedCluster(
	ctx context.Context,
	obj *v1alpha1.ClusterInstance,
) []reconcile.Request {
	// For HostedControlPlane clusters, we want to trigger reconciliation
	// when the ClusterInstance is created/updated
	if obj.Spec.ClusterType != v1alpha1.ClusterTypeHostedControlPlane {
		return []reconcile.Request{}
	}

	// The HostedCluster has the same name and namespace as the ClusterInstance
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		},
	}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *HostedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {

	//nolint:wrapcheck
	return ctrl.NewControllerManagedBy(mgr).
		Named("hostedClusterReconciler").
		For(&hypershiftv1beta1.HostedCluster{},
			// watch for create and update event for HostedCluster
			builder.WithPredicates(predicate.Funcs{
				GenericFunc: func(e event.GenericEvent) bool { return false },
				CreateFunc: func(e event.CreateEvent) bool {
					return isOwnedByClusterInstance(e.Object.GetLabels())
				},
				DeleteFunc: func(e event.DeleteEvent) bool { return false },
				UpdateFunc: func(e event.UpdateEvent) bool {
					return isOwnedByClusterInstance(e.ObjectNew.GetLabels())
				},
			})).
		WatchesRawSource(source.TypedKind(mgr.GetCache(),
			&v1alpha1.ClusterInstance{},
			handler.TypedEnqueueRequestsFromMapFunc(r.mapClusterInstanceToHostedCluster),
		)).
		Complete(r)
}
