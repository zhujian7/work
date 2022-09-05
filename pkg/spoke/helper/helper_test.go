package helper

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openshift/library-go/pkg/operator/events/eventstesting"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/diff"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	workapiv1 "open-cluster-management.io/api/work/v1"
)

func TestDeleteAppliedResourcess(t *testing.T) {
	cases := []struct {
		name                                 string
		existingResources                    []runtime.Object
		resourcesToRemove                    []workapiv1.AppliedManifestResourceMeta
		expectedResourcesPendingFinalization []workapiv1.AppliedManifestResourceMeta
		owner                                metav1.OwnerReference
	}{
		{
			name: "skip if resource does not exist",
			resourcesToRemove: []workapiv1.AppliedManifestResourceMeta{
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns1", Name: "n1"}},
			},
			owner: metav1.OwnerReference{Name: "n1", UID: "a"},
		},
		{
			name: "skip if resource have different uid",
			existingResources: []runtime.Object{
				newSecret("ns1", "n1", false, "ns1-n1-xxx", metav1.OwnerReference{Name: "n1", UID: "a"}),
				newSecret("ns2", "n2", true, "ns2-n2-xxx", metav1.OwnerReference{Name: "n1", UID: "a"}),
			},
			resourcesToRemove: []workapiv1.AppliedManifestResourceMeta{
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns1", Name: "n1"}, UID: "ns1-n1"},
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns2", Name: "n2"}, UID: "ns2-n2"},
			},
			owner: metav1.OwnerReference{Name: "n1", UID: "a"},
		},
		{
			name: "delete resources",
			existingResources: []runtime.Object{
				newSecret("ns1", "n1", false, "ns1-n1", metav1.OwnerReference{Name: "n1", UID: "a"}),
				newSecret("ns2", "n2", false, "ns2-n2", metav1.OwnerReference{Name: "n2", UID: "b"}),
			},
			resourcesToRemove: []workapiv1.AppliedManifestResourceMeta{
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns1", Name: "n1"}, UID: "ns1-n1"},
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns2", Name: "n2"}, UID: "ns2-n2"},
			},
			expectedResourcesPendingFinalization: []workapiv1.AppliedManifestResourceMeta{
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns1", Name: "n1"}, UID: "ns1-n1"},
			},
			owner: metav1.OwnerReference{Name: "n1", UID: "a"},
		},
		{
			name: "skip without uid",
			existingResources: []runtime.Object{
				newSecret("ns1", "n1", false, "ns1-n1", metav1.OwnerReference{Name: "n1", UID: "a"}),
				newSecret("ns2", "n2", true, "ns2-n2", metav1.OwnerReference{Name: "n1", UID: "a"}),
			},
			resourcesToRemove: []workapiv1.AppliedManifestResourceMeta{
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns1", Name: "n1"}},
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns2", Name: "n2"}, UID: "ns2-n2"},
			},
			expectedResourcesPendingFinalization: []workapiv1.AppliedManifestResourceMeta{
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns2", Name: "n2"}, UID: "ns2-n2"},
			},
			owner: metav1.OwnerReference{Name: "n1", UID: "a"},
		},
		{
			name: "skip if it is now owned",
			existingResources: []runtime.Object{
				newSecret("ns1", "n1", false, "ns1-n1", metav1.OwnerReference{Name: "n2", UID: "b"}),
			},
			resourcesToRemove: []workapiv1.AppliedManifestResourceMeta{
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns1", Name: "n1"}, UID: "ns1-n1"},
			},
			expectedResourcesPendingFinalization: []workapiv1.AppliedManifestResourceMeta{},
			owner:                                metav1.OwnerReference{Name: "n1", UID: "a"},
		},
		{
			name: "skip with multiple owners",
			existingResources: []runtime.Object{
				newSecret("ns1", "n1", false, "ns1-n1", metav1.OwnerReference{Name: "n1", UID: "a"}, metav1.OwnerReference{Name: "n2", UID: "b"}),
			},
			resourcesToRemove: []workapiv1.AppliedManifestResourceMeta{
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns1", Name: "n1"}, UID: "ns1-n1"},
				{Version: "v1", ResourceIdentifier: workapiv1.ResourceIdentifier{Resource: "secrets", Namespace: "ns2", Name: "n2"}, UID: "ns2-n2"},
			},
			expectedResourcesPendingFinalization: []workapiv1.AppliedManifestResourceMeta{},
			owner:                                metav1.OwnerReference{Name: "n1", UID: "a"},
		},
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeDynamicClient := fakedynamic.NewSimpleDynamicClient(scheme, c.existingResources...)
			actual, err := DeleteAppliedResources(context.TODO(), c.resourcesToRemove, "testing", fakeDynamicClient, eventstesting.NewTestingEventRecorder(t), c.owner, nil, nil)
			if err != nil {
				t.Errorf("unexpected err: %v", err)
			}

			if !equality.Semantic.DeepEqual(actual, c.expectedResourcesPendingFinalization) {
				t.Errorf(diff.ObjectDiff(actual, c.expectedResourcesPendingFinalization))
			}
		})
	}
}

func TestApplyOwnerReferences(t *testing.T) {
	testCases := []struct {
		name     string
		existing []metav1.OwnerReference
		required metav1.OwnerReference

		wantPatch  bool
		wantOwners []metav1.OwnerReference
	}{
		{
			name:       "add a owner",
			required:   metav1.OwnerReference{Name: "n1", UID: "a"},
			wantPatch:  true,
			wantOwners: []metav1.OwnerReference{{Name: "n1", UID: "a"}},
		},
		{
			name:       "append a owner",
			existing:   []metav1.OwnerReference{{Name: "n2", UID: "b"}},
			required:   metav1.OwnerReference{Name: "n1", UID: "a"},
			wantPatch:  true,
			wantOwners: []metav1.OwnerReference{{Name: "n2", UID: "b"}, {Name: "n1", UID: "a"}},
		},
		{
			name:       "remove a owner",
			existing:   []metav1.OwnerReference{{Name: "n2", UID: "b"}, {Name: "n1", UID: "a"}},
			required:   metav1.OwnerReference{Name: "n1", UID: "a-"},
			wantPatch:  true,
			wantOwners: []metav1.OwnerReference{{Name: "n2", UID: "b"}},
		},
		{
			name:      "remove a non existing owner",
			existing:  []metav1.OwnerReference{{Name: "n2", UID: "b"}, {Name: "n1", UID: "a"}},
			required:  metav1.OwnerReference{Name: "n3", UID: "c-"},
			wantPatch: false,
		},
		{
			name:      "append an existing owner",
			existing:  []metav1.OwnerReference{{Name: "n2", UID: "b"}, {Name: "n1", UID: "a"}},
			required:  metav1.OwnerReference{Name: "n1", UID: "a"},
			wantPatch: false,
		},
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			object := newSecret("ns1", "n1", false, "ns1-n1", c.existing...)
			fakeClient := fakedynamic.NewSimpleDynamicClient(scheme, object)
			gvr := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
			err := ApplyOwnerReferences(context.TODO(), fakeClient, gvr, object, c.required)
			if err != nil {
				t.Errorf("apply err: %v", err)
			}

			actions := fakeClient.Actions()
			if !c.wantPatch {
				if len(actions) > 0 {
					t.Fatalf("expect not patch but got %v", actions)
				}
				return
			}

			if len(actions) != 1 {
				t.Fatalf("expect patch action but got %v", actions)
			}

			patch := actions[0].(clienttesting.PatchAction).GetPatch()
			patchedObject := &metav1.PartialObjectMetadata{}
			err = json.Unmarshal(patch, patchedObject)
			if err != nil {
				t.Fatalf("failed to marshal patch: %v", err)
			}

			if !equality.Semantic.DeepEqual(c.wantOwners, patchedObject.GetOwnerReferences()) {
				t.Errorf("want ownerrefs %v, but got %v", c.wantOwners, patchedObject.GetOwnerReferences())
			}
		})
	}
}

func newSecret(namespace, name string, terminated bool, uid string, owner ...metav1.OwnerReference) *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			OwnerReferences: owner,
		},
	}

	if terminated {
		now := metav1.Now()
		secret.DeletionTimestamp = &now
	}
	if uid != "" {
		secret.UID = types.UID(uid)
	}

	return secret
}
