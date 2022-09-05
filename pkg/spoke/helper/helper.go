package helper

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	workapiv1 "open-cluster-management.io/api/work/v1"
	"open-cluster-management.io/work/pkg/helper"
	"open-cluster-management.io/work/pkg/spoke/auth"
)

// DeleteAppliedResources deletes all given applied resources and returns those pending for finalization
// If the uid recorded in resources is different from what we get by client, ignore the deletion.
func DeleteAppliedResources(
	ctx context.Context,
	resources []workapiv1.AppliedManifestResourceMeta,
	reason string,
	dynamicClient dynamic.Interface,
	recorder events.Recorder,
	owner metav1.OwnerReference,
	kubeClient kubernetes.Interface, executor *workapiv1.ManifestWorkExecutor) ([]workapiv1.AppliedManifestResourceMeta, []error) {
	var resourcesPendingFinalization []workapiv1.AppliedManifestResourceMeta
	var errs []error

	// set owner to be removed
	ownerCopy := owner.DeepCopy()
	ownerCopy.UID = types.UID(fmt.Sprintf("%s-", owner.UID))

	// We hard coded the delete policy to Background
	// TODO: reivist if user needs to set other options. Setting to Orphan may not make sense, since when
	// the manifestwork is removed, there is no way to track the orphaned resource any more.
	deletePolicy := metav1.DeletePropagationBackground

	validator := auth.NewExecutorValidator(kubeClient)

	for _, resource := range resources {
		gvr := schema.GroupVersionResource{Group: resource.Group, Version: resource.Version, Resource: resource.Resource}
		u, err := dynamicClient.
			Resource(gvr).
			Namespace(resource.Namespace).
			Get(ctx, resource.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			klog.V(2).Infof("Resource %v with key %s/%s is removed Successfully", gvr, resource.Namespace, resource.Name)
			continue
		}

		if err != nil {
			errs = append(errs, fmt.Errorf(
				"failed to get resource %v with key %s/%s: %w",
				gvr, resource.Namespace, resource.Name, err))
			continue
		}

		existingOwner := u.GetOwnerReferences()

		// If it is not owned by us, skip
		if !helper.IsOwnedBy(owner, existingOwner) {
			continue
		}

		err = validator.ValidateDelete(ctx, executor, gvr, resource.Namespace, resource.Name)
		if err != nil {
			if !apierrors.IsForbidden(err) {
				errs = append(errs, fmt.Errorf(
					"failed to check delete permission for resource %v with key %s/%s: %w",
					gvr, resource.Namespace, resource.Name, err))
				continue
			}
			// TODO: consider to reflect the not allowed error on the manifest work condition
			klog.Errorf("Resource %v with key %s/%s is not allowed to delete", gvr, resource.Namespace, resource.Name)
		}
		// If there are still any other existing owners (not only ManifestWorks), update ownerrefs only.
		if len(existingOwner) > 1 {
			err := ApplyOwnerReferences(ctx, dynamicClient, gvr, u, *ownerCopy)
			if err != nil {
				errs = append(errs, fmt.Errorf(
					"failed to remove owner from resource %v with key %s/%s: %w",
					gvr, resource.Namespace, resource.Name, err))
			}

			continue
		}

		if resource.UID != string(u.GetUID()) {
			// the traced instance has been deleted, and forget this item.
			continue
		}

		if u.GetDeletionTimestamp() != nil && !u.GetDeletionTimestamp().IsZero() {
			resourcesPendingFinalization = append(resourcesPendingFinalization, resource)
			continue
		}

		// delete the resource which is not deleted yet
		uid := types.UID(resource.UID)
		err = dynamicClient.
			Resource(gvr).
			Namespace(resource.Namespace).
			Delete(context.TODO(), resource.Name, metav1.DeleteOptions{
				Preconditions: &metav1.Preconditions{
					UID: &uid,
				},
				PropagationPolicy: &deletePolicy,
			})
		if errors.IsNotFound(err) {
			continue
		}
		// forget this item if the UID precondition check fails
		if errors.IsConflict(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf(
				"failed to delete resource %v with key %s/%s: %w",
				gvr, resource.Namespace, resource.Name, err))
			continue
		}

		resourcesPendingFinalization = append(resourcesPendingFinalization, resource)
		recorder.Eventf("ResourceDeleted", "Deleted resource %v with key %s/%s because %s.", gvr, resource.Namespace, resource.Name, reason)
	}

	return resourcesPendingFinalization, errs
}

func ApplyOwnerReferences(ctx context.Context, dynamicClient dynamic.Interface, gvr schema.GroupVersionResource, existing runtime.Object, requiredOwner metav1.OwnerReference) error {
	accessor, err := meta.Accessor(existing)
	if err != nil {
		return fmt.Errorf("type %t cannot be accessed: %v", existing, err)
	}
	patch := &unstructured.Unstructured{}
	patch.SetUID(accessor.GetUID())
	patch.SetResourceVersion(accessor.GetResourceVersion())
	patch.SetOwnerReferences([]metav1.OwnerReference{requiredOwner})

	modified := false
	patchedOwner := accessor.GetOwnerReferences()
	resourcemerge.MergeOwnerRefs(&modified, &patchedOwner, []metav1.OwnerReference{requiredOwner})
	patch.SetOwnerReferences(patchedOwner)

	if !modified {
		return nil
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	klog.V(2).Infof("Patching resource %v %s/%s with patch %s", gvr, accessor.GetNamespace(), accessor.GetName(), string(patchData))
	_, err = dynamicClient.Resource(gvr).Namespace(accessor.GetNamespace()).Patch(ctx, accessor.GetName(), types.MergePatchType, patchData, metav1.PatchOptions{})
	return err
}
