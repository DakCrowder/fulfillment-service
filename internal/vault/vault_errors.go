/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package vault

import (
	"errors"
	"net/http"

	vaultapi "github.com/hashicorp/vault/api"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// ToGrpcError converts a vault error to an appropriate gRPC status error.
func ToGrpcError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, vaultapi.ErrSecretNotFound) {
		return grpcstatus.Errorf(grpccodes.NotFound,
			"secret not found in vault: %v", err)
	}

	var responseErr *vaultapi.ResponseError
	if errors.As(err, &responseErr) {
		switch responseErr.StatusCode {
		case http.StatusForbidden:
			return grpcstatus.Errorf(grpccodes.PermissionDenied,
				"vault access denied: %v", err)
		case http.StatusNotFound:
			return grpcstatus.Errorf(grpccodes.NotFound,
				"secret not found in vault: %v", err)
		case http.StatusTooManyRequests:
			return grpcstatus.Errorf(grpccodes.ResourceExhausted,
				"vault rate limit exceeded: %v", err)
		case http.StatusServiceUnavailable:
			return grpcstatus.Errorf(grpccodes.Unavailable,
				"vault unavailable: %v", err)
		case http.StatusInternalServerError:
			return grpcstatus.Errorf(grpccodes.Internal,
				"vault internal error: %v", err)
		}
	}

	return grpcstatus.Errorf(grpccodes.Unavailable,
		"vault operation failed: %v", err)
}
