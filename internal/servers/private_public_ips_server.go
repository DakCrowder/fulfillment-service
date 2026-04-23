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
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database"
)

// PrivatePublicIPsServerBuilder contains the data and logic needed to create a PrivatePublicIPsServer.
type PrivatePublicIPsServerBuilder struct {
	logger            *slog.Logger
	notifier          *database.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.PublicIPsServer = (*PrivatePublicIPsServer)(nil)

// PrivatePublicIPsServer implements the private PublicIPs gRPC service. It delegates most operations to a
// GenericServer and adds pool-required validation on Create.
type PrivatePublicIPsServer struct {
	privatev1.UnimplementedPublicIPsServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.PublicIP]
}

// NewPrivatePublicIPsServer creates a builder that can then be used to configure and create a PrivatePublicIPsServer.
func NewPrivatePublicIPsServer() *PrivatePublicIPsServerBuilder {
	return &PrivatePublicIPsServerBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *PrivatePublicIPsServerBuilder) SetLogger(value *slog.Logger) *PrivatePublicIPsServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier that will be used to send change notifications.
func (b *PrivatePublicIPsServerBuilder) SetNotifier(value *database.Notifier) *PrivatePublicIPsServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the logic that will be used to determine the creators for objects. This is mandatory.
func (b *PrivatePublicIPsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivatePublicIPsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic that will be used to determine the tenants for objects.
func (b *PrivatePublicIPsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivatePublicIPsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivatePublicIPsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivatePublicIPsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build uses the configuration stored in the builder to create a new PrivatePublicIPsServer.
func (b *PrivatePublicIPsServerBuilder) Build() (result *PrivatePublicIPsServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.PublicIP]().
		SetLogger(b.logger).
		SetService(privatev1.PublicIPs_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivatePublicIPsServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivatePublicIPsServer) List(ctx context.Context,
	request *privatev1.PublicIPsListRequest) (response *privatev1.PublicIPsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivatePublicIPsServer) Get(ctx context.Context,
	request *privatev1.PublicIPsGetRequest) (response *privatev1.PublicIPsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivatePublicIPsServer) Create(ctx context.Context,
	request *privatev1.PublicIPsCreateRequest) (response *privatev1.PublicIPsCreateResponse, err error) {
	publicIP := request.GetObject()

	// Validate before creating:
	err = s.validatePublicIP(ctx, publicIP)
	if err != nil {
		return
	}

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivatePublicIPsServer) Update(ctx context.Context,
	request *privatev1.PublicIPsUpdateRequest) (response *privatev1.PublicIPsUpdateResponse, err error) {
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivatePublicIPsServer) Delete(ctx context.Context,
	request *privatev1.PublicIPsDeleteRequest) (response *privatev1.PublicIPsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivatePublicIPsServer) Signal(ctx context.Context,
	request *privatev1.PublicIPsSignalRequest) (response *privatev1.PublicIPsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// validatePublicIP validates the PublicIP object before creation.
func (s *PrivatePublicIPsServer) validatePublicIP(ctx context.Context,
	publicIP *privatev1.PublicIP) error {
	if publicIP == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "public IP is mandatory")
	}
	spec := publicIP.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "public IP spec is mandatory")
	}
	if spec.GetPool() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.pool' is required")
	}
	return nil
}
