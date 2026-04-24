/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/database"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Private public IPs server", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Prepare the database pool:
		db := server.MakeDatabase()
		DeferCleanup(db.Close)
		pool, err := pgxpool.New(ctx, db.MakeURL())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)

		// Create the transaction manager:
		tm, err := database.NewTxManager().
			SetLogger(logger).
			SetPool(pool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Start a transaction and add it to the context:
		tx, err := tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := tm.End(ctx, tx)
			Expect(err).ToNot(HaveOccurred())
		})
		ctx = database.TxIntoContext(ctx, tx)

		// Create the tables:
		err = dao.CreateTables[*privatev1.PublicIP](ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivatePublicIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivatePublicIPsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivatePublicIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivatePublicIPsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Validation tests", func() {
		var publicIPsServer *PrivatePublicIPsServer

		BeforeEach(func() {
			var err error

			// Create the server:
			publicIPsServer, err = NewPrivatePublicIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		Context("Pool required validation", func() {
			It("rejects nil object on Create", func() {
				_, err := publicIPsServer.Create(ctx, privatev1.PublicIPsCreateRequest_builder{}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("public IP is mandatory"))
			})

			It("rejects nil spec on Create", func() {
				_, err := publicIPsServer.Create(ctx, privatev1.PublicIPsCreateRequest_builder{
					Object: privatev1.PublicIP_builder{
						Metadata: privatev1.Metadata_builder{
							Tenants: []string{"shared"},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("public IP spec is mandatory"))
			})

			It("rejects empty pool on Create", func() {
				_, err := publicIPsServer.Create(ctx, privatev1.PublicIPsCreateRequest_builder{
					Object: privatev1.PublicIP_builder{
						Metadata: privatev1.Metadata_builder{
							Tenants: []string{"shared"},
						}.Build(),
						Spec: privatev1.PublicIPSpec_builder{}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("spec.pool"))
			})

			It("accepts valid pool on Create", func() {
				response, err := publicIPsServer.Create(ctx, privatev1.PublicIPsCreateRequest_builder{
					Object: privatev1.PublicIP_builder{
						Metadata: privatev1.Metadata_builder{
							Tenants: []string{"shared"},
						}.Build(),
						Spec: privatev1.PublicIPSpec_builder{
							Pool: "test-pool",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			})
		})
	})

	Describe("CRUD operations", func() {
		var publicIPsServer *PrivatePublicIPsServer

		BeforeEach(func() {
			var err error

			// Create the server:
			publicIPsServer, err = NewPrivatePublicIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates PublicIP and generates ID", func() {
			response, err := publicIPsServer.Create(ctx, privatev1.PublicIPsCreateRequest_builder{
				Object: privatev1.PublicIP_builder{
					Metadata: privatev1.Metadata_builder{
						Tenants: []string{"shared"},
					}.Build(),
					Spec: privatev1.PublicIPSpec_builder{
						Pool: "my-pool",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetMetadata()).ToNot(BeNil())
			Expect(object.GetMetadata().GetCreationTimestamp()).ToNot(BeNil())
		})

		It("retrieves PublicIP by ID", func() {
			createResponse, err := publicIPsServer.Create(ctx, privatev1.PublicIPsCreateRequest_builder{
				Object: privatev1.PublicIP_builder{
					Metadata: privatev1.Metadata_builder{
						Tenants: []string{"shared"},
					}.Build(),
					Spec: privatev1.PublicIPSpec_builder{
						Pool: "my-pool",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			created := createResponse.GetObject()

			getResponse, err := publicIPsServer.Get(ctx, privatev1.PublicIPsGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			retrieved := getResponse.GetObject()
			Expect(proto.Equal(created, retrieved)).To(BeTrue())
		})

		It("lists PublicIPs", func() {
			const count = 3
			for i := range count {
				_, err := publicIPsServer.Create(ctx, privatev1.PublicIPsCreateRequest_builder{
					Object: privatev1.PublicIP_builder{
						Metadata: privatev1.Metadata_builder{
							Tenants: []string{"shared"},
						}.Build(),
						Spec: privatev1.PublicIPSpec_builder{
							Pool: fmt.Sprintf("pool-%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := publicIPsServer.List(ctx, privatev1.PublicIPsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("updates PublicIP", func() {
			createResponse, err := publicIPsServer.Create(ctx, privatev1.PublicIPsCreateRequest_builder{
				Object: privatev1.PublicIP_builder{
					Metadata: privatev1.Metadata_builder{
						Name:    "original-name",
						Tenants: []string{"shared"},
					}.Build(),
					Spec: privatev1.PublicIPSpec_builder{
						Pool: "my-pool",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Update the name:
			object.GetMetadata().Name = "updated-name"
			updateResponse, err := publicIPsServer.Update(ctx, privatev1.PublicIPsUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Verify via Get:
			getResponse, err := publicIPsServer.Get(ctx, privatev1.PublicIPsGetRequest_builder{
				Id: updateResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetName()).To(Equal("updated-name"))
		})

		It("soft deletes PublicIP", func() {
			createResponse, err := publicIPsServer.Create(ctx, privatev1.PublicIPsCreateRequest_builder{
				Object: privatev1.PublicIP_builder{
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{"test-finalizer"},
						Tenants:    []string{"shared"},
					}.Build(),
					Spec: privatev1.PublicIPSpec_builder{
						Pool: "my-pool",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete:
			_, err = publicIPsServer.Delete(ctx, privatev1.PublicIPsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Verify soft delete (deletion_timestamp set, object still retrievable):
			getResponse, err := publicIPsServer.Get(ctx, privatev1.PublicIPsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("signals PublicIP", func() {
			createResponse, err := publicIPsServer.Create(ctx, privatev1.PublicIPsCreateRequest_builder{
				Object: privatev1.PublicIP_builder{
					Metadata: privatev1.Metadata_builder{
						Tenants: []string{"shared"},
					}.Build(),
					Spec: privatev1.PublicIPSpec_builder{
						Pool: "my-pool",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = publicIPsServer.Signal(ctx, privatev1.PublicIPsSignalRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
