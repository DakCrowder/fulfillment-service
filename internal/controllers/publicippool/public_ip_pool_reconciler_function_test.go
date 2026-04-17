/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package publicippool

import (
	"context"
	"errors"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/gvks"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/labels"
)

var _ = Describe("buildSpec", func() {
	It("Maps IPv4 family to spec.ipv4.cidrs", func() {
		t := &task{
			publicIPPool: privatev1.PublicIPPool_builder{
				Id: "pool-ipv4",
				Spec: privatev1.PublicIPPoolSpec_builder{
					Cidrs:    []string{"203.0.113.0/24", "198.51.100.0/24"},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec).To(HaveKey("ipv4"))
		Expect(spec).ToNot(HaveKey("ipv6"))
		ipv4 := spec["ipv4"].(map[string]any)
		Expect(ipv4["cidrs"]).To(Equal([]any{"203.0.113.0/24", "198.51.100.0/24"}))
	})

	It("Maps IPv6 family to spec.ipv6.cidrs", func() {
		t := &task{
			publicIPPool: privatev1.PublicIPPool_builder{
				Id: "pool-ipv6",
				Spec: privatev1.PublicIPPoolSpec_builder{
					Cidrs:    []string{"2001:db8::/32"},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV6,
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec).To(HaveKey("ipv6"))
		Expect(spec).ToNot(HaveKey("ipv4"))
		ipv6 := spec["ipv6"].(map[string]any)
		Expect(ipv6["cidrs"]).To(Equal([]any{"2001:db8::/32"}))
	})

	It("Produces neither ipv4 nor ipv6 key when family is unspecified", func() {
		t := &task{
			publicIPPool: privatev1.PublicIPPool_builder{
				Id: "pool-unspecified",
				Spec: privatev1.PublicIPPoolSpec_builder{
					Cidrs:    []string{"10.0.0.0/8"},
					IpFamily: privatev1.IPFamily_IP_FAMILY_UNSPECIFIED,
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec).ToNot(HaveKey("ipv4"))
		Expect(spec).ToNot(HaveKey("ipv6"))
	})
})

// newPublicIPPoolCR creates an unstructured PublicIPPool CR for use with the fake client.
func newPublicIPPoolCR(id, namespace, name string, deletionTimestamp *metav1.Time) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvks.PublicIPPool)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	obj.SetLabels(map[string]string{
		labels.PublicIPPoolUuid: id,
	})
	if deletionTimestamp != nil {
		obj.SetDeletionTimestamp(deletionTimestamp)
		obj.SetFinalizers([]string{"osac.openshift.io/publicippool"})
	}
	return obj
}

// hasFinalizer checks if the fulfillment-controller finalizer is present on the public IP pool.
func hasFinalizer(pool *privatev1.PublicIPPool) bool {
	return slices.Contains(pool.GetMetadata().GetFinalizers(), finalizers.Controller)
}

// newTaskForDelete creates a task configured for testing delete() with hub-dependent paths.
func newTaskForDelete(poolID, hubID string, hubCache controllers.HubCache) *task {
	pool := privatev1.PublicIPPool_builder{
		Id: poolID,
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{finalizers.Controller},
		}.Build(),
		Status: privatev1.PublicIPPoolStatus_builder{
			Hub: hubID,
		}.Build(),
	}.Build()

	f := &function{
		logger:   logger,
		hubCache: hubCache,
	}

	return &task{
		r:            f,
		publicIPPool: pool,
	}
}

var _ = Describe("delete", func() {
	const (
		poolID       = "pool-delete-id"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
		crName       = "publicippool-test"
	)

	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("should remove finalizer when K8s object doesn't exist", func() {
		scheme := runtime.NewScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		t := newTaskForDelete(poolID, hubID, hubCache)
		Expect(hasFinalizer(t.publicIPPool)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.publicIPPool)).To(BeFalse())
	})

	It("should call hubClient.Delete when K8s object exists without DeletionTimestamp", func() {
		cr := newPublicIPPoolCR(poolID, hubNamespace, crName, nil)

		scheme := runtime.NewScheme()
		scheme.AddKnownTypeWithName(
			schema.GroupVersionKind{Group: gvks.PublicIPPool.Group, Version: gvks.PublicIPPool.Version, Kind: gvks.PublicIPPool.Kind + "List"},
			&unstructured.UnstructuredList{},
		)

		deleteCalled := false
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.DeleteOption) error {
					deleteCalled = true
					return nil
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		t := newTaskForDelete(poolID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeTrue())
		// Finalizer should NOT be removed — K8s object still exists
		Expect(hasFinalizer(t.publicIPPool)).To(BeTrue())
	})

	It("should not call hubClient.Delete when K8s object has DeletionTimestamp", func() {
		now := metav1.Now()
		cr := newPublicIPPoolCR(poolID, hubNamespace, crName, &now)

		scheme := runtime.NewScheme()
		scheme.AddKnownTypeWithName(
			schema.GroupVersionKind{Group: gvks.PublicIPPool.Group, Version: gvks.PublicIPPool.Version, Kind: gvks.PublicIPPool.Kind + "List"},
			&unstructured.UnstructuredList{},
		)

		deleteCalled := false
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.DeleteOption) error {
					deleteCalled = true
					return nil
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		t := newTaskForDelete(poolID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeFalse())
		// Finalizer should NOT be removed — K8s object still being deleted
		Expect(hasFinalizer(t.publicIPPool)).To(BeTrue())
	})

	It("should propagate error when hub cache returns error", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, errors.New("hub not found"))

		t := newTaskForDelete(poolID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("hub not found"))
		// Finalizer should NOT be removed on error
		Expect(hasFinalizer(t.publicIPPool)).To(BeTrue())
	})

	It("should remove finalizer when no hub is assigned", func() {
		pool := privatev1.PublicIPPool_builder{
			Id: poolID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.PublicIPPoolStatus_builder{
				// No hub assigned
			}.Build(),
		}.Build()

		f := &function{
			logger: logger,
		}

		t := &task{
			r:            f,
			publicIPPool: pool,
		}

		Expect(hasFinalizer(t.publicIPPool)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.publicIPPool)).To(BeFalse())
	})
})

var _ = Describe("validateTenant", func() {
	It("should succeed when exactly one tenant is assigned", func() {
		pool := privatev1.PublicIPPool_builder{
			Metadata: privatev1.Metadata_builder{
				Tenants: []string{"tenant-1"},
			}.Build(),
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		err := t.validateTenant()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail when no tenants are assigned", func() {
		pool := privatev1.PublicIPPool_builder{
			Metadata: privatev1.Metadata_builder{
				Tenants: []string{},
			}.Build(),
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("exactly one tenant"))
	})

	It("should fail when multiple tenants are assigned", func() {
		pool := privatev1.PublicIPPool_builder{
			Metadata: privatev1.Metadata_builder{
				Tenants: []string{"tenant-1", "tenant-2"},
			}.Build(),
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("exactly one tenant"))
	})

	It("should fail when metadata is missing", func() {
		pool := privatev1.PublicIPPool_builder{}.Build()

		t := &task{
			publicIPPool: pool,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("exactly one tenant"))
	})
})

var _ = Describe("setDefaults", func() {
	It("should set PENDING state when status is unspecified", func() {
		pool := privatev1.PublicIPPool_builder{
			Id: "pool-defaults",
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		t.setDefaults()

		Expect(t.publicIPPool.GetStatus().GetState()).To(Equal(privatev1.PublicIPPoolState_PUBLIC_IP_POOL_STATE_PENDING))
	})

	It("should not overwrite existing state", func() {
		pool := privatev1.PublicIPPool_builder{
			Id: "pool-existing-state",
			Status: privatev1.PublicIPPoolStatus_builder{
				State: privatev1.PublicIPPoolState_PUBLIC_IP_POOL_STATE_READY,
			}.Build(),
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		t.setDefaults()

		Expect(t.publicIPPool.GetStatus().GetState()).To(Equal(privatev1.PublicIPPoolState_PUBLIC_IP_POOL_STATE_READY))
	})

	It("should create status if it doesn't exist", func() {
		pool := privatev1.PublicIPPool_builder{
			Id: "pool-no-status",
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		Expect(t.publicIPPool.HasStatus()).To(BeFalse())

		t.setDefaults()

		Expect(t.publicIPPool.HasStatus()).To(BeTrue())
		Expect(t.publicIPPool.GetStatus().GetState()).To(Equal(privatev1.PublicIPPoolState_PUBLIC_IP_POOL_STATE_PENDING))
	})
})

var _ = Describe("addFinalizer", func() {
	It("should add finalizer when not present", func() {
		pool := privatev1.PublicIPPool_builder{
			Id: "pool-no-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{},
			}.Build(),
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(hasFinalizer(t.publicIPPool)).To(BeTrue())
	})

	It("should not add finalizer when already present", func() {
		pool := privatev1.PublicIPPool_builder{
			Id: "pool-has-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		added := t.addFinalizer()

		Expect(added).To(BeFalse())
		Expect(hasFinalizer(t.publicIPPool)).To(BeTrue())
		// Should not duplicate
		Expect(len(t.publicIPPool.GetMetadata().GetFinalizers())).To(Equal(1))
	})

	It("should create metadata if it doesn't exist", func() {
		pool := privatev1.PublicIPPool_builder{
			Id: "pool-no-metadata",
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		Expect(t.publicIPPool.HasMetadata()).To(BeFalse())

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(t.publicIPPool.HasMetadata()).To(BeTrue())
		Expect(hasFinalizer(t.publicIPPool)).To(BeTrue())
	})
})

var _ = Describe("removeFinalizer", func() {
	It("should remove finalizer when present", func() {
		pool := privatev1.PublicIPPool_builder{
			Id: "pool-has-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller, "other-finalizer"},
			}.Build(),
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		Expect(hasFinalizer(t.publicIPPool)).To(BeTrue())

		t.removeFinalizer()

		Expect(hasFinalizer(t.publicIPPool)).To(BeFalse())
		// Other finalizers should remain
		Expect(t.publicIPPool.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when finalizer not present", func() {
		pool := privatev1.PublicIPPool_builder{
			Id: "pool-no-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{"other-finalizer"},
			}.Build(),
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		Expect(hasFinalizer(t.publicIPPool)).To(BeFalse())

		t.removeFinalizer()

		Expect(hasFinalizer(t.publicIPPool)).To(BeFalse())
		Expect(t.publicIPPool.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when metadata doesn't exist", func() {
		pool := privatev1.PublicIPPool_builder{
			Id: "pool-no-metadata",
		}.Build()

		t := &task{
			publicIPPool: pool,
		}

		// Should not panic
		t.removeFinalizer()

		Expect(t.publicIPPool.HasMetadata()).To(BeFalse())
	})
})
