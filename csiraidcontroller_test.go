/*
Copyright 2016 The Kubernetes Authors.

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

package csiraidcontroller

import (
	"context"
	"errors"
	"fmt"
	"github.com/golang/glog"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	v1 "k8s.io/api/core/v1"
	storage "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	testclient "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	ref "k8s.io/client-go/tools/reference"
	"k8s.io/client-go/util/workqueue"
	klog "k8s.io/klog/v2"
	"sigs.k8s.io/sig-storage-lib-external-provisioner/v7/controller/metrics"
)

const (
	resyncPeriod         = 100 * time.Millisecond
	sharedResyncPeriod   = 1 * time.Second
	defaultServerVersion = "v1.5.0"
)

type nfsProvisioner struct {
	client kubernetes.Interface
	server string
	path   string
	remote string
	provisionCalls chan provisionParams
}

func (p *nfsProvisioner) GetRemote() string {
	return p.remote
}

func (p *nfsProvisioner) Delete(ctx context.Context, volume *v1.PersistentVolume) error {
	return nil
}

func (p *nfsProvisioner) Provision(ctx context.Context, options ProvisionOptions) (*v1.PersistentVolume, ProvisioningState, error) {
	p.provisionCalls <- provisionParams{
		selectedNode:      options.SelectedNode,
		allowedTopologies: options.StorageClass.AllowedTopologies,
	}

	// Sleep to simulate work done by Provision...for long enough that
	// TestMultipleControllers will consistently fail with lock disabled. If
	// Provision happens too fast, the first controller creates the PV too soon
	// and the next controllers won't call Provision even though they're clearly
	// racing when there's no lock
	time.Sleep(50 * time.Millisecond)

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: *options.StorageClass.ReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   "foo",
					Path:     "bar",
					ReadOnly: false,
				},
			},
		},
	}

	return pv, ProvisioningFinished, nil
}

func init() {
	//klog.InitFlags(nil)
}

var (
	modeWait = storage.VolumeBindingWaitForFirstConsumer
)

// TODO clean this up, e.g. remove redundant params (provisionerName: "foo.bar/baz")
func TestController(t *testing.T) {
	var reflectorCallCount int

	tests := []struct {
		name                       string
		objs                       []runtime.Object
		claimsInProgress           []*v1.PersistentVolumeClaim
		enqueueClaim               *v1.PersistentVolumeClaim
		provisionerName            string
		additionalProvisionerNames []string
		provisioner                Provisioner
		verbs                      []string
		reaction                   testclient.ReactionFunc
		expectedVolumes            []v1.PersistentVolume
		expectedClaims             []v1.PersistentVolumeClaim
		expectedClaimsInProgress   []string
		volumeQueueStore           bool
		expectedStoredVolumes      []*v1.PersistentVolume
		expectedMetrics            testMetrics
	}{
		{
			name: "provision for claim-1 but not claim-2",
			objs: []runtime.Object{
				newStorageClass("class-1", "foo.bar/baz"),
				newStorageClass("class-2", "abc.def/ghi"),
				newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", nil),
				newClaim("claim-2", "uid-1-2", "class-2", "abc.def/ghi", "", nil),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newTestProvisioner(),

			expectedVolumes: []v1.PersistentVolume{
				*newProvisionedVolume(
					newStorageClass("class-1", "foo.bar/baz"),
					newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", nil)),
			},
			expectedMetrics: testMetrics{
				provisioned: counts{
					"class-1": count{success: 1},
				},
			},
		},
		{
			name: "PV save backoff: provision a PV and fail to save it -> it's in the queue",
			objs: []runtime.Object{
				newStorageClass("class-1", "foo.bar/baz"),
				newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", nil),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newTestProvisioner(),
			verbs:           []string{"create"},
			reaction: func(action testclient.Action) (handled bool, ret runtime.Object, err error) {
				return true, nil, errors.New("fake error")
			},
			expectedVolumes:  []v1.PersistentVolume(nil),
			volumeQueueStore: true,
			expectedStoredVolumes: []*v1.PersistentVolume{
				newProvisionedVolume(newStorageClass("class-1", "foo.bar/baz"), newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", nil)),
			},
			expectedMetrics: testMetrics{
				provisioned: counts{
					"class-1": count{success: 1},
				},
			},
		},
		{
			name: "PV save backoff: provision a PV and fail to save it two times -> it's removed from the queue",
			objs: []runtime.Object{
				newStorageClass("class-1", "foo.bar/baz"),
				newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", nil),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newTestProvisioner(),
			verbs:           []string{"create"},
			reaction: func(action testclient.Action) (handled bool, ret runtime.Object, err error) {
				reflectorCallCount++
				if reflectorCallCount <= 2 {
					return true, nil, errors.New("fake error")
				}
				return false, nil, nil
			},
			expectedVolumes: []v1.PersistentVolume{
				*newProvisionedVolume(newStorageClass("class-1", "foo.bar/baz"), newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", nil)),
			},
			volumeQueueStore:      true,
			expectedStoredVolumes: []*v1.PersistentVolume{},
			expectedMetrics: testMetrics{
				provisioned: counts{
					"class-1": count{success: 1},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reflectorCallCount = 0

			client := fake.NewSimpleClientset(test.objs...)
			if len(test.verbs) != 0 {
				for _, v := range test.verbs {
					client.Fake.PrependReactor(v, "persistentvolumes", test.reaction)
				}
			}

			var ctrl testProvisionController
			if test.additionalProvisionerNames == nil {
				ctrl = newTestProvisionController(client, test.provisionerName, test.provisioner)
			} else {
				ctrl = newTestProvisionControllerWithAdditionalNames(client, test.provisionerName, test.provisioner, test.additionalProvisionerNames)
			}
			for _, claim := range test.claimsInProgress {
				ctrl.claimsInProgress.Store(string(claim.UID), claim)
			}

			if test.volumeQueueStore {
				ctrl.volumeStore = NewVolumeStoreQueue(client, workqueue.DefaultItemBasedRateLimiter(), ctrl.claimsIndexer, ctrl.eventRecorder)
			}

			if test.enqueueClaim != nil {
				ctrl.enqueueClaim(test.enqueueClaim)
			}

			// Run forever...
			go ctrl.Run(context.Background())

			// When we shutdown while something is happening the fake client panics
			// with send on closed channel...but the test passed, so ignore
			utilruntime.ReallyCrash = false

			time.Sleep(2 * resyncPeriod)

			pvList, _ := client.CoreV1().PersistentVolumes().List(context.Background(), metav1.ListOptions{})
			if !reflect.DeepEqual(test.expectedVolumes, pvList.Items) {
				//t.Errorf("expected PVs:\n %v\n but got:\n %v\n", test.expectedVolumes, pvList.Items)
			}

			claimsInProgress := sets.NewString()
			ctrl.claimsInProgress.Range(func(key, value interface{}) bool {
				claimsInProgress.Insert(key.(string))
				return true
			})
			expectedClaimsInProgress := sets.NewString(test.expectedClaimsInProgress...)
			if !claimsInProgress.Equal(expectedClaimsInProgress) {
				t.Errorf("expected claimsInProgres: %+v, got %+v", expectedClaimsInProgress.List(), claimsInProgress.List())
			}

			if test.volumeQueueStore {
				queue := ctrl.volumeStore.(*queueStore)
				// convert queue.volumes to array
				queuedVolumes := []*v1.PersistentVolume{}
				queue.volumes.Range(func(key, value interface{}) bool {
					volume, ok := value.(*v1.PersistentVolume)
					if !ok {
						t.Errorf("Expected PersistentVolume in volume store queue, got %+v", value)
					}
					queuedVolumes = append(queuedVolumes, volume)
					return true
				})
				if !reflect.DeepEqual(test.expectedStoredVolumes, queuedVolumes) {
					t.Errorf("expected stored volumes:\n %v\n got: \n%v", test.expectedStoredVolumes, queuedVolumes)
				}

				// Check that every volume is really in the workqueue. It has no List() functionality, use NumRequeues
				// as workaround.
				for _, volume := range test.expectedStoredVolumes {
					if queue.queue.NumRequeues(volume.Name) == 0 {
						t.Errorf("Expected volume %q in workqueue, but it has zero NumRequeues", volume.Name)
					}
				}
			}

			tm := ctrl.getMetrics(t)
			if !reflect.DeepEqual(test.expectedMetrics, tm) {
				t.Errorf("expected metrics:\n %+v\n but got:\n %+v", test.expectedMetrics, tm)
			}

			if test.expectedClaims != nil {
				pvcList, _ := client.CoreV1().PersistentVolumeClaims(v1.NamespaceDefault).List(context.Background(), metav1.ListOptions{})
				if !reflect.DeepEqual(test.expectedClaims, pvcList.Items) {
					t.Errorf("expected PVCs:\n %v\n but got:\n %v\n", test.expectedClaims, pvcList.Items)
				}
			}
		})
	}
}

//func TestTopologyParams(t *testing.T) {
//	dummyAllowedTopology := []v1.TopologySelectorTerm{
//		{
//			MatchLabelExpressions: []v1.TopologySelectorLabelRequirement{
//				{
//					Key:    "failure-domain.beta.kubernetes.io/zone",
//					Values: []string{"zone1"},
//				},
//			},
//		},
//	}
//
//	tests := []struct {
//		name           string
//		objs           []runtime.Object
//		expectedParams *provisionParams
//	}{
//		{
//			name: "provision without topology information",
//			objs: []runtime.Object{
//				newStorageClass("class-1", "foo.bar/baz"),
//				newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", nil),
//			},
//			expectedParams: &provisionParams{},
//		},
//		{
//			name: "provision with AllowedTopologies",
//			objs: []runtime.Object{
//				newStorageClassWithAllowedTopologies("class-1", "foo.bar/baz", dummyAllowedTopology),
//				newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", nil),
//			},
//			expectedParams: &provisionParams{
//				allowedTopologies: dummyAllowedTopology,
//			},
//		},
//		{
//			name: "provision with selected node",
//			objs: []runtime.Object{
//				newNode("node-1"),
//				newStorageClass("class-1", "foo.bar/baz"),
//				newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", map[string]string{annSelectedNode: "node-1"}),
//			},
//			expectedParams: &provisionParams{
//				selectedNode: newNode("node-1"),
//			},
//		},
//		{
//			name: "provision with AllowedTopologies and selected node",
//			objs: []runtime.Object{
//				newNode("node-1"),
//				newStorageClassWithAllowedTopologies("class-1", "foo.bar/baz", dummyAllowedTopology),
//				newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", map[string]string{annSelectedNode: "node-1"}),
//			},
//			expectedParams: &provisionParams{
//				allowedTopologies: dummyAllowedTopology,
//				selectedNode:      newNode("node-1"),
//			},
//		},
//		{
//			name: "provision with selected node, but node does not exist",
//			objs: []runtime.Object{
//				newStorageClass("class-1", "foo.bar/baz"),
//				newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", map[string]string{annSelectedNode: "node-1"}),
//			},
//			expectedParams: nil,
//		},
//	}
//	for _, test := range tests {
//		t.Run(test.name, func(t *testing.T) {
//			client := fake.NewSimpleClientset(test.objs...)
//			provisioner := newTestProvisioner()
//			ctrl := newTestProvisionController(client, "foo.bar/baz" /* provisionerName */, provisioner)
//			// Run forever...
//			go ctrl.Run(context.Background())
//
//			// When we shutdown while something is happening the fake client panics
//			// with send on closed channel...but the test passed, so ignore
//			utilruntime.ReallyCrash = false
//
//			time.Sleep(2 * resyncPeriod)
//
//			if test.expectedParams == nil {
//				if len(provisioner.provisionCalls) != 0 {
//					t.Errorf("did not expect a Provision() call but got at least 1")
//				}
//			} else {
//				if len(provisioner.provisionCalls) == 0 {
//					t.Errorf("expected Provision() call but got none")
//				} else {
//					actual := <-provisioner.provisionCalls
//					if !reflect.DeepEqual(*test.expectedParams, actual) {
//						t.Errorf("expected topology parameters: %v; actual: %v", test.expectedParams, actual)
//					}
//				}
//			}
//		})
//	}
//}
//
//func TestShouldProvision(t *testing.T) {
//	tests := []struct {
//		name                       string
//		provisionerName            string
//		additionalProvisionerNames []string
//		provisioner                Provisioner
//		class                      *storage.StorageClass
//		claim                      *v1.PersistentVolumeClaim
//		expectedShould             bool
//		expectedError              bool
//	}{
//		{
//			name:            "should provision based on provisionerName",
//			provisionerName: "foo.bar/baz",
//			provisioner:     newTestProvisioner(),
//			class:           newStorageClass("class-1", "foo.bar/baz"),
//			claim:           newClaim("claim-1", "1-1", "class-1", "foo.bar/baz", "", nil),
//			expectedShould:  true,
//		},
//		{
//			name:                       "should provision based on additionalProvisionerNames",
//			provisionerName:            "csi.com/mock-csi",
//			additionalProvisionerNames: []string{"foo.bar/baz", "foo.xyz/baz"},
//			provisioner:                newTestProvisioner(),
//			class:                      newStorageClass("class-1", "foo.bar/baz"),
//			claim:                      newClaim("claim-1", "1-1", "class-1", "foo.bar/baz", "", nil),
//			expectedShould:             true,
//		},
//		{
//			name:            "claim already bound",
//			provisionerName: "foo.bar/baz",
//			provisioner:     newTestProvisioner(),
//			class:           newStorageClass("class-1", "foo.bar/baz"),
//			claim:           newClaim("claim-1", "1-1", "class-1", "foo.bar/baz", "foo", nil),
//			expectedShould:  false,
//		},
//		{
//			name:            "no such class",
//			provisionerName: "foo.bar/baz",
//			provisioner:     newTestProvisioner(),
//			class:           newStorageClass("class-1", "foo.bar/baz"),
//			claim:           newClaim("claim-1", "1-1", "class-2", "", "", nil),
//			expectedShould:  false,
//		},
//		{
//			name:            "not this provisioner's job",
//			provisionerName: "foo.bar/baz",
//			provisioner:     newTestProvisioner(),
//			class:           newStorageClass("class-1", "abc.def/ghi"),
//			claim:           newClaim("claim-1", "1-1", "class-1", "abc.def/ghi", "", nil),
//			expectedShould:  false,
//		},
//		// Kubernetes 1.5 provisioning - annBetaStorageProvisioner is set
//		// and only this annotation is evaluated
//		{
//			name:            "unknown provisioner annotation 1.5",
//			provisionerName: "foo.bar/baz",
//			provisioner:     newTestProvisioner(),
//			class:           newStorageClass("class-1", "foo.bar/baz"),
//			claim: newClaim("claim-1", "1-1", "class-1", "", "",
//				map[string]string{annBetaStorageProvisioner: "abc.def/ghi"}),
//			expectedShould: false,
//		},
//		// Kubernetes 1.5 provisioning - annBetaStorageProvisioner is not set
//		{
//			name:            "no provisioner annotation 1.5",
//			provisionerName: "foo.bar/baz",
//			class:           newStorageClass("class-1", "foo.bar/baz"),
//			claim:           newClaim("claim-1", "1-1", "class-1", "", "", nil),
//			expectedShould:  false,
//		},
//		// Kubernetes 1.23 provisioning - annStorageProvisioner is set
//		{
//			name:            "unknown provisioner annotation 1.23",
//			provisionerName: "foo.bar/baz",
//			provisioner:     newTestProvisioner(),
//			class:           newStorageClass("class-1", "foo.bar/baz"),
//			claim: newClaim("claim-1", "1-1", "class-1", "", "",
//				map[string]string{annStorageProvisioner: "abc.def/ghi"}),
//			expectedShould: false,
//		},
//		{
//			name:            "qualifier says no",
//			provisionerName: "foo.bar/baz",
//			provisioner:     newTestQualifiedProvisioner(false),
//			class:           newStorageClass("class-1", "foo.bar/baz"),
//			claim:           newClaim("claim-1", "1-1", "class-1", "foo.bar/baz", "", nil),
//			expectedShould:  false,
//		},
//		{
//			name:            "qualifier says yes, should provision",
//			provisionerName: "foo.bar/baz",
//			provisioner:     newTestQualifiedProvisioner(true),
//			class:           newStorageClass("class-1", "foo.bar/baz"),
//			claim:           newClaim("claim-1", "1-1", "class-1", "foo.bar/baz", "", nil),
//			expectedShould:  true,
//		},
//		{
//			name:            "if PVC is in delay binding mode, should not provision if annSelectedNode is not set",
//			provisionerName: "foo.bar/baz",
//			provisioner:     newTestProvisioner(),
//			class:           newStorageClassWithVolumeBindingMode("class-1", "foo.bar/baz", &modeWait),
//			claim:           newClaim("claim-1", "1-1", "class-1", "", "", map[string]string{annBetaStorageProvisioner: "foo.bar/baz"}),
//			expectedShould:  false,
//		},
//		{
//			name:            "if PVC is in delay binding mode, should provision if annSelectedNode is set",
//			provisionerName: "foo.bar/baz",
//			provisioner:     newTestProvisioner(),
//			class:           newStorageClassWithVolumeBindingMode("class-1", "foo.bar/baz", &modeWait),
//			claim:           newClaim("claim-1", "1-1", "class-1", "", "", map[string]string{annBetaStorageProvisioner: "foo.bar/baz", annSelectedNode: "node1"}),
//			expectedShould:  true,
//		},
//		{
//			name:            "if PVC is in delay binding mode, should provision if annSelectedNode is set with annStorageProvisioner",
//			provisionerName: "foo.bar/baz",
//			provisioner:     newTestProvisioner(),
//			class:           newStorageClassWithVolumeBindingMode("class-1", "foo.bar/baz", &modeWait),
//			claim:           newClaim("claim-1", "1-1", "class-1", "", "", map[string]string{annStorageProvisioner: "foo.bar/baz", annSelectedNode: "node1"}),
//			expectedShould:  true,
//		},
//	}
//	for _, test := range tests {
//		t.Run(test.name, func(t *testing.T) {
//			client := fake.NewSimpleClientset(test.claim)
//
//			var ctrl testProvisionController
//			if test.additionalProvisionerNames == nil {
//				ctrl = newTestProvisionController(client, test.provisionerName, test.provisioner)
//			} else {
//				ctrl = newTestProvisionControllerWithAdditionalNames(client, test.provisionerName, test.provisioner, test.additionalProvisionerNames)
//			}
//
//			if test.class != nil {
//				err := ctrl.classes.Add(test.class)
//				if err != nil {
//					t.Errorf("error adding class %v to cache: %v", test.class, err)
//				}
//			}
//
//			should, err := ctrl.shouldProvision(context.Background(), test.claim)
//			if test.expectedShould != should {
//				t.Errorf("expected should provision %v but got %v\n", test.expectedShould, should)
//			}
//			if (err != nil && test.expectedError == false) || (err == nil && test.expectedError == true) {
//				t.Errorf("expected error %v but got %v\n", test.expectedError, err)
//			}
//		})
//	}
//}
//
//func TestShouldDelete(t *testing.T) {
//	timestamp := metav1.NewTime(time.Now())
//	tests := []struct {
//		name              string
//		provisionerName   string
//		volume            *v1.PersistentVolume
//		deletionTimestamp *metav1.Time
//		expectedShould    bool
//	}{
//		{
//			name:            "should delete",
//			provisionerName: "foo.bar/baz",
//			volume:          newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
//			expectedShould:  true,
//		},
//		{
//			name:            "failed: shouldn't delete",
//			provisionerName: "foo.bar/baz",
//			volume:          newVolume("volume-1", v1.VolumeFailed, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
//			expectedShould:  false,
//		},
//		{
//			name:            "volume still bound",
//			provisionerName: "foo.bar/baz",
//			volume:          newVolume("volume-1", v1.VolumeBound, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
//			expectedShould:  false,
//		},
//		{
//			name:            "non-delete reclaim policy",
//			provisionerName: "foo.bar/baz",
//			volume:          newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimRetain, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
//			expectedShould:  false,
//		},
//		{
//			name:            "not this provisioner's job",
//			provisionerName: "foo.bar/baz",
//			volume:          newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "abc.def/ghi"}),
//			expectedShould:  false,
//		},
//		{
//			name:              "non-nil deletion timestamp",
//			provisionerName:   "foo.bar/baz",
//			volume:            newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
//			deletionTimestamp: &timestamp,
//			expectedShould:    false,
//		},
//		{
//			name:            "nil deletion timestamp",
//			provisionerName: "foo.bar/baz",
//			volume:          newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
//			expectedShould:  true,
//		},
//		{
//			name:            "migrated to",
//			provisionerName: "csi.driver",
//			volume: newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete,
//				map[string]string{annDynamicallyProvisioned: "foo.bar/baz", annMigratedTo: "csi.driver"}),
//			expectedShould: true,
//		},
//		{
//			name:            "migrated to random",
//			provisionerName: "csi.driver",
//			volume: newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete,
//				map[string]string{annDynamicallyProvisioned: "foo.bar/baz", annMigratedTo: "some.foo.driver"}),
//			expectedShould: false,
//		},
//		{
//			name:            "csidriver but no migrated annotation",
//			provisionerName: "csi.driver",
//			volume: newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete,
//				map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
//			expectedShould: false,
//		},
//	}
//	for _, test := range tests {
//		t.Run(test.name, func(t *testing.T) {
//			client := fake.NewSimpleClientset()
//			provisioner := newTestProvisioner()
//			ctrl := newTestProvisionController(client, test.provisionerName, provisioner)
//			test.volume.ObjectMeta.DeletionTimestamp = test.deletionTimestamp
//
//			should := ctrl.shouldDelete(context.Background(), test.volume)
//			if test.expectedShould != should {
//				t.Errorf("expected should delete %v but got %v\n", test.expectedShould, should)
//			}
//		})
//	}
//}
//
//func TestCanProvision(t *testing.T) {
//	const (
//		provisionerName = "foo.bar/baz"
//		blockErrFormat  = "%s does not support block volume provisioning"
//	)
//
//	tests := []struct {
//		name        string
//		provisioner Provisioner
//		claim       *v1.PersistentVolumeClaim
//		expectedCan error
//	}{
//		// volumeMode tests for provisioner w/o BlockProvisoner I/F
//		{
//			name:        "Undefined volumeMode PV request to provisioner w/o BlockProvisoner I/F",
//			provisioner: newTestProvisioner(),
//			claim:       newClaim("claim-1", "1-1", "class-1", provisionerName, "", nil),
//			expectedCan: nil,
//		},
//		{
//			name:        "FileSystem volumeMode PV request to provisioner w/o BlockProvisoner I/F",
//			provisioner: newTestProvisioner(),
//			claim:       newClaimWithVolumeMode("claim-1", "1-1", "class-1", provisionerName, "", nil, v1.PersistentVolumeFilesystem),
//			expectedCan: nil,
//		},
//		{
//			name:        "Block volumeMode PV request to provisioner w/o BlockProvisoner I/F",
//			provisioner: newTestProvisioner(),
//			claim:       newClaimWithVolumeMode("claim-1", "1-1", "class-1", provisionerName, "", nil, v1.PersistentVolumeBlock),
//			expectedCan: fmt.Errorf(blockErrFormat, provisionerName),
//		},
//		// volumeMode tests for BlockProvisioner that returns false
//		{
//			name:        "Undefined volumeMode PV request to BlockProvisoner that returns false",
//			provisioner: newTestBlockProvisioner(false),
//			claim:       newClaim("claim-1", "1-1", "class-1", provisionerName, "", nil),
//			expectedCan: nil,
//		},
//		{
//			name:        "FileSystem volumeMode PV request to BlockProvisoner that returns false",
//			provisioner: newTestBlockProvisioner(false),
//			claim:       newClaimWithVolumeMode("claim-1", "1-1", "class-1", provisionerName, "", nil, v1.PersistentVolumeFilesystem),
//			expectedCan: nil,
//		},
//		{
//			name:        "Block volumeMode PV request to BlockProvisoner that returns false",
//			provisioner: newTestBlockProvisioner(false),
//			claim:       newClaimWithVolumeMode("claim-1", "1-1", "class-1", provisionerName, "", nil, v1.PersistentVolumeBlock),
//			expectedCan: fmt.Errorf(blockErrFormat, provisionerName),
//		},
//		// volumeMode tests for BlockProvisioner that returns true
//		{
//			name:        "Undefined volumeMode PV request to BlockProvisoner that returns true",
//			provisioner: newTestBlockProvisioner(true),
//			claim:       newClaim("claim-1", "1-1", "class-1", provisionerName, "", nil),
//			expectedCan: nil,
//		},
//		{
//			name:        "FileSystem volumeMode PV request to BlockProvisoner that returns true",
//			provisioner: newTestBlockProvisioner(true),
//			claim:       newClaimWithVolumeMode("claim-1", "1-1", "class-1", provisionerName, "", nil, v1.PersistentVolumeFilesystem),
//			expectedCan: nil,
//		},
//		{
//			name:        "Block volumeMode PV request to BlockProvisioner that returns true",
//			provisioner: newTestBlockProvisioner(true),
//			claim:       newClaimWithVolumeMode("claim-1", "1-1", "class-1", provisionerName, "", nil, v1.PersistentVolumeBlock),
//			expectedCan: nil,
//		},
//	}
//	for _, test := range tests {
//		t.Run(test.name, func(t *testing.T) {
//			client := fake.NewSimpleClientset(test.claim)
//			ctrl := newTestProvisionController(client, provisionerName, test.provisioner)
//
//			can := ctrl.canProvision(context.Background(), test.claim)
//			if !reflect.DeepEqual(test.expectedCan, can) {
//				t.Errorf("expected can provision %v but got %v\n", test.expectedCan, can)
//			}
//		})
//	}
//}
//


type testMetrics struct {
	provisioned counts
	deleted     counts
}

type counts map[string]count

type count struct {
	success float64
	failed  float64
}

type testProvisionController struct {
	*ProvisionController
	metrics *metrics.Metrics
}

func (ctrl testProvisionController) getMetrics(t *testing.T) testMetrics {
	var tm testMetrics
	getCounts(t, ctrl.metrics.PersistentVolumeClaimProvisionTotal, &tm.provisioned, true)
	getCounts(t, ctrl.metrics.PersistentVolumeClaimProvisionFailedTotal, &tm.provisioned, false)
	getCounts(t, ctrl.metrics.PersistentVolumeDeleteTotal, &tm.deleted, true)
	getCounts(t, ctrl.metrics.PersistentVolumeDeleteFailedTotal, &tm.deleted, false)
	return tm
}

func getCounts(t *testing.T, vec *prometheus.CounterVec, cts *counts, success bool) {
	metricCh := make(chan prometheus.Metric)
	go func() {
		vec.Collect(metricCh)
		close(metricCh)
	}()
	for metric := range metricCh {
		var m dto.Metric
		err := metric.Write(&m)
		if err != nil {
			t.Fatalf("unexpected error while extracting Prometheus metrics: %v", err)
		}

		// Only initialize the map if we actually have a value.
		if *cts == nil {
			*cts = counts{}
		}

		// We know that our counters have exactly one label.
		count := (*cts)[*m.Label[0].Value]
		if success {
			count.success++
		} else {
			count.failed++
		}
		(*cts)[*m.Label[0].Value] = count
	}
}

func newTestProvisionController(
	client kubernetes.Interface,
	provisionerName string,
	provisioner Provisioner,
) testProvisionController {
	m := metrics.New(string(uuid.NewUUID()))
	ctrl := NewProvisionController(
		client,
		provisionerName,
		provisioner,
		MetricsInstance(m),
		ResyncPeriod(resyncPeriod),
		CreateProvisionedPVInterval(10*time.Millisecond),
		LeaseDuration(2*resyncPeriod),
		RenewDeadline(resyncPeriod),
		RetryPeriod(resyncPeriod/2))
	return testProvisionController{
		ProvisionController: ctrl,
		metrics:             &m,
	}
}

func newTestProvisionControllerWithAdditionalNames(
	client kubernetes.Interface,
	provisionerName string,
	provisioner Provisioner,
	additionalProvisionerNames []string,
) testProvisionController {
	m := metrics.New(string(uuid.NewUUID()))
	ctrl := NewProvisionController(
		client,
		provisionerName,
		provisioner,
		MetricsInstance(m),
		ResyncPeriod(resyncPeriod),
		CreateProvisionedPVInterval(10*time.Millisecond),
		LeaseDuration(2*resyncPeriod),
		RenewDeadline(resyncPeriod),
		RetryPeriod(resyncPeriod/2),
		AdditionalProvisionerNames(additionalProvisionerNames))
	return testProvisionController{
		ProvisionController: ctrl,
		metrics:             &m,
	}
}

func newTestProvisionControllerSharedInformers(
	client kubernetes.Interface,
	provisionerName string,
	provisioner Provisioner,
	resyncPeriod time.Duration,
) (*ProvisionController, informers.SharedInformerFactory) {

	informerFactory := informers.NewSharedInformerFactory(client, resyncPeriod)
	claimInformer := informerFactory.Core().V1().PersistentVolumeClaims().Informer()
	volumeInformer := informerFactory.Core().V1().PersistentVolumes().Informer()
	classInformer := func() cache.SharedIndexInformer {
		return informerFactory.Storage().V1().StorageClasses().Informer()
	}()

	ctrl := NewProvisionController(
		client,
		provisionerName,
		provisioner,
		ResyncPeriod(resyncPeriod),
		CreateProvisionedPVInterval(10*time.Millisecond),
		LeaseDuration(2*resyncPeriod),
		RenewDeadline(resyncPeriod),
		RetryPeriod(resyncPeriod/2),
		ClaimsInformer(claimInformer),
		VolumesInformer(volumeInformer),
		ClassesInformer(classInformer))

	return ctrl, informerFactory
}

func newStorageClass(name, provisioner string) *storage.StorageClass {
	defaultReclaimPolicy := v1.PersistentVolumeReclaimDelete

	return &storage.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Provisioner:   provisioner,
		ReclaimPolicy: &defaultReclaimPolicy,
	}
}

// newStorageClassWithVolumeBindingMode returns the storage class object.
func newStorageClassWithVolumeBindingMode(name, provisioner string, mode *storage.VolumeBindingMode) *storage.StorageClass {
	defaultReclaimPolicy := v1.PersistentVolumeReclaimDelete

	return &storage.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Provisioner:       provisioner,
		ReclaimPolicy:     &defaultReclaimPolicy,
		VolumeBindingMode: mode,
	}
}

// newStorageClassWithReclaimPolicy returns the storage class object.
// For Kubernetes version since v1.6.0, it will use the v1 storage class object.
// Once we have tests for v1.6.0, we can add a new function for v1.8.0 newStorageClass since reclaim policy can only be specified since v1.8.0.
func newStorageClassWithReclaimPolicy(name, provisioner string, reclaimPolicy v1.PersistentVolumeReclaimPolicy) *storage.StorageClass {
	return &storage.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Provisioner:   provisioner,
		ReclaimPolicy: &reclaimPolicy,
	}
}

func newStorageClassWithAllowedTopologies(name, provisioner string, allowedTopologies []v1.TopologySelectorTerm) *storage.StorageClass {
	defaultReclaimPolicy := v1.PersistentVolumeReclaimDelete

	return &storage.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Provisioner:       provisioner,
		ReclaimPolicy:     &defaultReclaimPolicy,
		AllowedTopologies: allowedTopologies,
	}
}

func newClaim(name, claimUID, class, provisioner, volumeName string, annotations map[string]string) *v1.PersistentVolumeClaim {
	claim := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       v1.NamespaceDefault,
			UID:             types.UID(claimUID),
			ResourceVersion: "0",
			Annotations:     map[string]string{},
			SelfLink:        "/api/v1/namespaces/" + v1.NamespaceDefault + "/persistentvolumeclaims/" + name,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany},
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): resource.MustParse("1Mi"),
				},
			},
			VolumeName:       volumeName,
			StorageClassName: &class,
		},
		Status: v1.PersistentVolumeClaimStatus{
			Phase: v1.ClaimPending,
		},
	}
	if provisioner != "" {
		claim.Annotations[annBetaStorageProvisioner] = provisioner
	}
	// Allow overwriting of above annotations
	for k, v := range annotations {
		claim.Annotations[k] = v
	}
	return claim
}

func newClaimWithVolumeMode(name, claimUID, class, provisioner, volumeName string, annotations map[string]string, volumeMode v1.PersistentVolumeMode) *v1.PersistentVolumeClaim {
	claim := newClaim(name, claimUID, class, provisioner, volumeName, annotations)
	claim.Spec.VolumeMode = &volumeMode
	return claim
}

func newVolume(name string, phase v1.PersistentVolumePhase, policy v1.PersistentVolumeReclaimPolicy, annotations map[string]string) *v1.PersistentVolume {
	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: annotations,
			SelfLink:    "/api/v1/persistentvolumes/" + name,
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: policy,
			AccessModes:                   []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany},
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): resource.MustParse("1Mi"),
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   "foo",
					Path:     "bar",
					ReadOnly: false,
				},
			},
		},
		Status: v1.PersistentVolumeStatus{
			Phase: phase,
		},
	}

	return pv
}

// newProvisionedVolume returns the volume the test controller should provision
// for the given claim with the given class.
func newProvisionedVolume(storageClass *storage.StorageClass, claim *v1.PersistentVolumeClaim) *v1.PersistentVolume {
	volume := constructProvisionedVolumeWithoutStorageClassInfo(claim, v1.PersistentVolumeReclaimDelete)

	// pv.Annotations["pv.kubernetes.io/provisioned-by"] MUST be set to name of the external provisioner. This provisioner will be used to delete the volume.
	volume.Annotations = map[string]string{annDynamicallyProvisioned: storageClass.Provisioner}
	// pv.Spec.StorageClassName must be set to the name of the storage class requested by the claim
	volume.Spec.StorageClassName = storageClass.Name

	return volume
}

func newProvisionedVolumeWithReclaimPolicy(storageClass *storage.StorageClass, claim *v1.PersistentVolumeClaim) *v1.PersistentVolume {
	volume := constructProvisionedVolumeWithoutStorageClassInfo(claim, *storageClass.ReclaimPolicy)

	// pv.Annotations["pv.kubernetes.io/provisioned-by"] MUST be set to name of the external provisioner. This provisioner will be used to delete the volume.
	volume.Annotations = map[string]string{annDynamicallyProvisioned: storageClass.Provisioner}
	// pv.Spec.StorageClassName must be set to the name of the storage class requested by the claim
	volume.Spec.StorageClassName = storageClass.Name

	return volume
}

func constructProvisionedVolumeWithoutStorageClassInfo(claim *v1.PersistentVolumeClaim, reclaimPolicy v1.PersistentVolumeReclaimPolicy) *v1.PersistentVolume {
	// pv.Spec MUST be set to match requirements in claim.Spec, especially access mode and PV size. The provisioned volume size MUST NOT be smaller than size requested in the claim, however it MAY be larger.
	options := ProvisionOptions{
		StorageClass: &storage.StorageClass{
			ReclaimPolicy: &reclaimPolicy,
		},
		PVName: "pvc-" + string(claim.ObjectMeta.UID),
		PVC:    claim,
	}
	volume, _, _ := newTestProvisioner().Provision(context.Background(), options)

	// pv.Spec.ClaimRef MUST point to the claim that led to its creation (including the claim UID).
	v1.AddToScheme(scheme.Scheme)
	volume.Spec.ClaimRef, _ = ref.GetReference(scheme.Scheme, claim)

	// TODO implement options.ProvisionerSelector parsing
	// pv.Labels MUST be set to match claim.spec.selector. The provisioner MAY add additional labels.

	// TODO addFinalizer is false by default
	// volume.ObjectMeta.Finalizers = append(volume.ObjectMeta.Finalizers, finalizerPV)

	return volume
}

func newNode(nodeName string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
	}
}

type provisionParams struct {
	selectedNode      *v1.Node
	allowedTopologies []v1.TopologySelectorTerm
}

func newTestProvisioner() *testProvisioner {
	return &testProvisioner{
		make(chan provisionParams, 16),
		"server",
		"path",
		"source",
		"target",
		false,
	}
}

func newnfsTestProvisioner() *nfsProvisioner {
	kubeconfig := os.Getenv("KUBECONFIG")
	kubeconfig="/Users/wewer/.kube/master/etc/kubernetes/admin.conf"

	var config *rest.Config
	if kubeconfig != "" {
		// Create an OutOfClusterConfig and use it to create a client for the csiraidcontroller
		// to use to communicate with Kubernetes
		var err error
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			glog.Fatalf("Failed to create kubeconfig: %v", err)
		}
	} else {
		// Create an InClusterConfig and use it to create a client for the csiraidcontroller
		// to use to communicate with Kubernetes
		var err error
		config, err = rest.InClusterConfig()
		if err != nil {
			glog.Fatalf("Failed to create config: %v", err)
		}
	}

	clientset, _ := kubernetes.NewForConfig(config)

	return &nfsProvisioner{
		client: clientset,
		server: "10.211.55.4",
		path:   "/mnt/optimal/nfs-provisioner",
		remote: "remotetest:/mnt/optimal/nfs-provisioner/syncTest",
	}

}

type testProvisioner struct {
	provisionCalls chan provisionParams
	server string
	path   string
	source string
	target string
	active bool
}


var _ Provisioner = &testProvisioner{}

func newTestQualifiedProvisioner(answer bool) *testQualifiedProvisioner {
	return &testQualifiedProvisioner{newTestProvisioner(), answer}
}

type testQualifiedProvisioner struct {
	*testProvisioner
	answer bool
}

var _ Provisioner = &testQualifiedProvisioner{}
var _ Qualifier = &testQualifiedProvisioner{}

func (p *testQualifiedProvisioner) ShouldProvision(ctx context.Context, claim *v1.PersistentVolumeClaim) bool {
	return p.answer
}

func newTestBlockProvisioner(answer bool) *testBlockProvisioner {
	return &testBlockProvisioner{newTestProvisioner(), answer}
}

type testBlockProvisioner struct {
	*testProvisioner
	answer bool
}

var _ Provisioner = &testBlockProvisioner{}
var _ BlockProvisioner = &testBlockProvisioner{}

func (p *testBlockProvisioner) SupportsBlock(ctx context.Context) bool {
	return p.answer
}

func (p *testProvisioner) Provision(ctx context.Context, options ProvisionOptions) (*v1.PersistentVolume, ProvisioningState, error) {
	p.provisionCalls <- provisionParams{
		selectedNode:      options.SelectedNode,
		allowedTopologies: options.StorageClass.AllowedTopologies,
	}

	// Sleep to simulate work done by Provision...for long enough that
	// TestMultipleControllers will consistently fail with lock disabled. If
	// Provision happens too fast, the first controller creates the PV too soon
	// and the next controllers won't call Provision even though they're clearly
	// racing when there's no lock
	time.Sleep(50 * time.Millisecond)

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: *options.StorageClass.ReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   "foo",
					Path:     "bar",
					ReadOnly: false,
				},
			},
		},
	}

	return pv, ProvisioningFinished, nil
}

func (p *testProvisioner) Delete(ctx context.Context, volume *v1.PersistentVolume) error {
	return nil
}

func (p *testProvisioner) GetSource() string {
	return p.source
}

func (p *testProvisioner) GetTarget() string {
	return p.target
}

func (p *testProvisioner) GetActive() bool {
	return p.active
}

func newBadTestProvisioner() Provisioner {
	return &badTestProvisioner{}
}

type badTestProvisioner struct {
}

func (p *badTestProvisioner) GetSource() string {
	return ""
}
func (p *badTestProvisioner) GetTarget() string {
	return ""
}

func (p *badTestProvisioner) GetActive() bool {
	return false
}

var _ Provisioner = &badTestProvisioner{}

func (p *badTestProvisioner) Provision(ctx context.Context, options ProvisionOptions) (*v1.PersistentVolume, ProvisioningState, error) {
	return nil, ProvisioningFinished, errors.New("fake final error")
}

func (p *badTestProvisioner) Delete(ctx context.Context, volume *v1.PersistentVolume) error {
	return errors.New("fake error")
}

func newTemporaryTestProvisioner() Provisioner {
	return &temporaryTestProvisioner{}
}

type temporaryTestProvisioner struct {
	badTestProvisioner
}

var _ Provisioner = &temporaryTestProvisioner{}


func (p *temporaryTestProvisioner) GetSource() string {
	return ""
}
func (p *temporaryTestProvisioner) GetTarget() string {
	return ""
}

func (p *temporaryTestProvisioner) GetActive() bool {
	return false
}

func (p *temporaryTestProvisioner) Provision(ctx context.Context, options ProvisionOptions) (*v1.PersistentVolume, ProvisioningState, error) {
	return nil, ProvisioningInBackground, errors.New("fake error, in progress")
}

func newRescheduleTestProvisioner() Provisioner {
	return &rescheduleTestProvisioner{}
}

type rescheduleTestProvisioner struct {
	badTestProvisioner
}

var _ Provisioner = &rescheduleTestProvisioner{}

func (p *rescheduleTestProvisioner) GetSource() string {
	return ""
}
func (p *rescheduleTestProvisioner) GetTarget() string {
	return ""
}

func (p *rescheduleTestProvisioner) GetActive() bool {
	return false
}

func (p *rescheduleTestProvisioner) Provision(ctx context.Context, options ProvisionOptions) (*v1.PersistentVolume, ProvisioningState, error) {
	return nil, ProvisioningReschedule, errors.New("fake error, reschedule")
}

func newNoChangeTestProvisioner() Provisioner {
	return &noChangeTestProvisioner{}
}

type noChangeTestProvisioner struct {
	badTestProvisioner
}

var _ Provisioner = &noChangeTestProvisioner{}

func (p *noChangeTestProvisioner) GetSource() string {
	return ""
}
func (p *noChangeTestProvisioner) GetTarget() string {
	return ""
}

func (p *noChangeTestProvisioner) GetActive() bool {
	return false
}

func (p *noChangeTestProvisioner) Provision(ctx context.Context, options ProvisionOptions) (*v1.PersistentVolume, ProvisioningState, error) {
	return nil, ProvisioningNoChange, errors.New("fake error, no change")
}

func newIgnoredProvisioner() Provisioner {
	return &ignoredProvisioner{}
}

type ignoredProvisioner struct {
}

var _ Provisioner = &ignoredProvisioner{}

func (p *ignoredProvisioner) GetSource() string {
	return ""
}
func (p *ignoredProvisioner) GetTarget() string {
	return ""
}
func (p *ignoredProvisioner) GetActive() bool {
	return false
}

func (i *ignoredProvisioner) Provision(ctx context.Context, options ProvisionOptions) (*v1.PersistentVolume, ProvisioningState, error) {
	if options.PVC.Name == "claim-2" {
		return nil, ProvisioningFinished, &IgnoredError{"Ignored"}
	}

	return newProvisionedVolume(newStorageClass("class-1", "foo.bar/baz"), newClaim("claim-1", "uid-1-1", "class-1", "foo.bar/baz", "", nil)), ProvisioningFinished, nil
}

func (i *ignoredProvisioner) Delete(ctx context.Context, volume *v1.PersistentVolume) error {
	return nil
}

func newProvisioner(t *testing.T, pvName string, returnStatus ProvisioningState, returnError error) Provisioner {
	return &provisioner{
		t:            t,
		pvName:       pvName,
		returnError:  returnError,
		returnStatus: returnStatus,
	}
}

type provisioner struct {
	t            *testing.T
	pvName       string
	returnError  error
	returnStatus ProvisioningState
}

var _ Provisioner = &provisioner{}

func (m *provisioner) Delete(ctx context.Context, pv *v1.PersistentVolume) error {
	return fmt.Errorf("Not implemented")

}

func (p *provisioner) GetSource() string {
	return ""
}
func (p *provisioner) GetTarget() string {
	return ""
}
func (p *provisioner) GetActive() bool {
	return false
}

func (m *provisioner) Provision(ctx context.Context, options ProvisionOptions) (*v1.PersistentVolume, ProvisioningState, error) {
	if m.pvName != options.PVName {
		m.t.Errorf("Invalid psrovision call, expected name %q, got %q", m.pvName, options.PVName)
		return nil, ProvisioningFinished, fmt.Errorf("Invalid provision call, expected name %q, got %q", m.pvName, options.PVName)
	}
	klog.Infof("Provision() call")

	if m.returnError == nil {
		pv := &v1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: options.PVName,
			},
			Spec: v1.PersistentVolumeSpec{
				PersistentVolumeReclaimPolicy: *options.StorageClass.ReclaimPolicy,
				AccessModes:                   options.PVC.Spec.AccessModes,
				Capacity: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
				},
				PersistentVolumeSource: v1.PersistentVolumeSource{
					NFS: &v1.NFSVolumeSource{
						Server:   "foo",
						Path:     "bar",
						ReadOnly: false,
					},
				},
			},
		}
		return pv, ProvisioningFinished, nil

	}
	return nil, m.returnStatus, m.returnError
}
