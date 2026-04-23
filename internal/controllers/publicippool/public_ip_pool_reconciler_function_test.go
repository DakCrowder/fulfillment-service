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
	"github.com/osac-project/fulfillment-service/internal/kubernetes/annotations"
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

})

var _ = Describe("validateIPFamily", func() {
	It("should succeed for IPv4", func() {
		t := &task{
			publicIPPool: privatev1.PublicIPPool_builder{
				Spec: privatev1.PublicIPPoolSpec_builder{
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}

		err := t.validateIPFamily()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should succeed for IPv6", func() {
		t := &task{
			publicIPPool: privatev1.PublicIPPool_builder{
				Spec: privatev1.PublicIPPoolSpec_builder{
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV6,
				}.Build(),
			}.Build(),
		}

		err := t.validateIPFamily()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail for unspecified IP family", func() {
		t := &task{
			publicIPPool: privatev1.PublicIPPool_builder{
				Spec: privatev1.PublicIPPoolSpec_builder{
					IpFamily: privatev1.IPFamily_IP_FAMILY_UNSPECIFIED,
				}.Build(),
			}.Build(),
		}

		err := t.validateIPFamily()
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, errUnsupportedIPFamily)).To(BeTrue())
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
		Expect(errors.Is(err, errInvalidTenantCount)).To(BeTrue())
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
		Expect(errors.Is(err, errInvalidTenantCount)).To(BeTrue())
	})

	It("should fail when metadata is missing", func() {
		pool := privatev1.PublicIPPool_builder{}.Build()

		t := &task{
			publicIPPool: pool,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, errInvalidTenantCount)).To(BeTrue())
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

// newTaskForUpdate creates a task configured for testing update() with the K8s create/patch paths.
// The pool has a finalizer already present, one tenant, valid IP family, and a pre-set hub ID
func newTaskForUpdate(poolID, hubID string, hubCache controllers.HubCache) *task {
	pool := privatev1.PublicIPPool_builder{
		Id: poolID,
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{finalizers.Controller},
			Tenants:    []string{"tenant-1"},
		}.Build(),
		Spec: privatev1.PublicIPPoolSpec_builder{
			Cidrs:    []string{"203.0.113.0/24"},
			IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
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

// newSchemeWithPublicIPPoolList creates a runtime.Scheme that registers the PublicIPPool list GVK
// so the fake client can handle List operations.
func newSchemeWithPublicIPPoolList() *runtime.Scheme {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{
			Group:   gvks.PublicIPPool.Group,
			Version: gvks.PublicIPPool.Version,
			Kind:    gvks.PublicIPPool.Kind + "List",
		},
		&unstructured.UnstructuredList{},
	)
	return scheme
}

var _ = Describe("getKubeObject", func() {
	const (
		poolID       = "pool-kube-id"
		hubNamespace = "test-ns"
	)

	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("should return nil when no objects exist", func() {
		scheme := newSchemeWithPublicIPPoolList()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		t := &task{
			publicIPPool: privatev1.PublicIPPool_builder{Id: poolID}.Build(),
			hubClient:    fakeClient,
			hubNamespace: hubNamespace,
		}

		obj, err := t.getKubeObject(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj).To(BeNil())
	})

	It("should return the object when exactly one exists", func() {
		cr := newPublicIPPoolCR(poolID, hubNamespace, "publicippool-one", nil)
		scheme := newSchemeWithPublicIPPoolList()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr).
			Build()

		t := &task{
			publicIPPool: privatev1.PublicIPPool_builder{Id: poolID}.Build(),
			hubClient:    fakeClient,
			hubNamespace: hubNamespace,
		}

		obj, err := t.getKubeObject(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj).ToNot(BeNil())
		Expect(obj.GetName()).To(Equal("publicippool-one"))
	})

	It("should return ErrDuplicatePublicIPPool when multiple objects match", func() {
		cr1 := newPublicIPPoolCR(poolID, hubNamespace, "publicippool-dup-1", nil)
		cr2 := newPublicIPPoolCR(poolID, hubNamespace, "publicippool-dup-2", nil)
		scheme := newSchemeWithPublicIPPoolList()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr1, cr2).
			Build()

		t := &task{
			publicIPPool: privatev1.PublicIPPool_builder{Id: poolID}.Build(),
			hubClient:    fakeClient,
			hubNamespace: hubNamespace,
		}

		obj, err := t.getKubeObject(ctx)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, errDuplicatePublicIPPool)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("2"))
		Expect(obj).To(BeNil())
	})

	It("should propagate List error", func() {
		scheme := newSchemeWithPublicIPPoolList()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, client clnt.WithWatch, list clnt.ObjectList, opts ...clnt.ListOption) error {
					return errors.New("list failed")
				},
			}).
			Build()

		t := &task{
			publicIPPool: privatev1.PublicIPPool_builder{Id: poolID}.Build(),
			hubClient:    fakeClient,
			hubNamespace: hubNamespace,
		}

		obj, err := t.getKubeObject(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("list failed"))
		Expect(obj).To(BeNil())
	})
})

var _ = Describe("update", func() {
	const (
		poolID       = "pool-update-id"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
		crName       = "publicippool-existing"
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

	It("should return early after adding finalizer", func() {
		pool := privatev1.PublicIPPool_builder{
			Id: poolID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{},
			}.Build(),
		}.Build()

		t := &task{
			r:            &function{logger: logger},
			publicIPPool: pool,
		}

		Expect(hasFinalizer(t.publicIPPool)).To(BeFalse())

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.publicIPPool)).To(BeTrue())
	})

	It("should create K8s object when none exists", func() {
		scheme := newSchemeWithPublicIPPoolList()
		createCalled := false
		var createdObj *unstructured.Unstructured
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
					createCalled = true
					createdObj = obj.(*unstructured.Unstructured)
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

		t := newTaskForUpdate(poolID, hubID, hubCache)

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(createCalled).To(BeTrue())
		Expect(createdObj.GetGenerateName()).To(Equal(objectPrefix))
		Expect(createdObj.GetNamespace()).To(Equal(hubNamespace))
		Expect(createdObj.GetLabels()).To(HaveKeyWithValue(labels.PublicIPPoolUuid, poolID))
		Expect(createdObj.GetAnnotations()).To(HaveKeyWithValue(annotations.Tenant, "tenant-1"))
		Expect(createdObj.GroupVersionKind()).To(Equal(gvks.PublicIPPool))

		spec, found, err := unstructured.NestedMap(createdObj.Object, "spec")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(spec).To(HaveKey("ipv4"))
	})

	It("should include implementation strategy annotation when set", func() {
		scheme := newSchemeWithPublicIPPoolList()
		var createdObj *unstructured.Unstructured
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
					createdObj = obj.(*unstructured.Unstructured)
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

		t := newTaskForUpdate(poolID, hubID, hubCache)
		t.publicIPPool.GetSpec().SetImplementationStrategy("metallb-l2")

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(createdObj.GetAnnotations()).To(
			HaveKeyWithValue(implementationStrategyAnnotation, "metallb-l2"),
		)
	})

	It("should not include implementation strategy annotation when empty", func() {
		scheme := newSchemeWithPublicIPPoolList()
		var createdObj *unstructured.Unstructured
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
					createdObj = obj.(*unstructured.Unstructured)
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

		t := newTaskForUpdate(poolID, hubID, hubCache)

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(createdObj.GetAnnotations()).ToNot(HaveKey(implementationStrategyAnnotation))
	})

	It("should patch existing K8s object", func() {
		cr := newPublicIPPoolCR(poolID, hubNamespace, crName, nil)
		scheme := newSchemeWithPublicIPPoolList()

		patchCalled := false
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, patch clnt.Patch, opts ...clnt.PatchOption) error {
					patchCalled = true
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

		t := newTaskForUpdate(poolID, hubID, hubCache)

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(patchCalled).To(BeTrue())
	})

	It("should propagate Create error", func() {
		scheme := newSchemeWithPublicIPPoolList()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
					return errors.New("create failed")
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

		t := newTaskForUpdate(poolID, hubID, hubCache)

		err := t.update(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("create failed"))
	})

	It("should propagate Patch error", func() {
		cr := newPublicIPPoolCR(poolID, hubNamespace, crName, nil)
		scheme := newSchemeWithPublicIPPoolList()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, patch clnt.Patch, opts ...clnt.PatchOption) error {
					return errors.New("patch failed")
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

		t := newTaskForUpdate(poolID, hubID, hubCache)

		err := t.update(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("patch failed"))
	})

	It("should propagate getKubeObject error", func() {
		scheme := newSchemeWithPublicIPPoolList()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, client clnt.WithWatch, list clnt.ObjectList, opts ...clnt.ListOption) error {
					return errors.New("list failed")
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

		t := newTaskForUpdate(poolID, hubID, hubCache)

		err := t.update(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("list failed"))
	})

	It("should set hub ID in status", func() {
		scheme := newSchemeWithPublicIPPoolList()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
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

		t := newTaskForUpdate(poolID, hubID, hubCache)

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(t.publicIPPool.GetStatus().GetHub()).To(Equal(hubID))
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
