package manifestcontroller

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/openshift/library-go/pkg/operator/v1helpers"

	workapiv1 "open-cluster-management.io/api/work/v1"
)

type manifestworkEventHandler struct {
	enqueueFunc func(name string)
}

func (h *manifestworkEventHandler) OnAdd(obj interface{}) {
	mw, ok := obj.(*workapiv1.ManifestWork)
	if ok {
		h.enqueueFunc(mw.Name)
	}
}

func (h *manifestworkEventHandler) OnUpdate(oldObj, newObj interface{}) {
	new, okNew := newObj.(*workapiv1.ManifestWork)
	old, okOld := oldObj.(*workapiv1.ManifestWork)
	if okNew && okOld {
		if !v1helpers.IsConditionTrue(new.Status.Conditions, workapiv1.WorkAvailable) ||
			!v1helpers.IsConditionTrue(new.Status.Conditions, workapiv1.WorkApplied) {
			// the manifests are not applied successfully, requeue it
			h.enqueueFunc(new.Name)
			return
		}
		if !manifestWorkSpecEqual(new.Spec, old.Spec) {
			// the manifestwork spec is updated, requeue it
			h.enqueueFunc(new.Name)
			return
		}
	}
}

func (h *manifestworkEventHandler) OnDelete(obj interface{}) {
}

// manifestWorkEqual if two manifestworks' spec are equal, return true
// If they are not equal or there is any error happened, return false
func manifestWorkSpecEqual(newSpec, oldSpec workapiv1.ManifestWorkSpec) bool {
	newBytes, err := json.Marshal(newSpec)
	if err != nil {
		return false
	}
	oldBytes, err := json.Marshal(oldSpec)
	if err != nil {
		return false
	}

	newHash := sha256.Sum256(newBytes)
	oldHash := sha256.Sum256(oldBytes)
	return newHash == oldHash
}
