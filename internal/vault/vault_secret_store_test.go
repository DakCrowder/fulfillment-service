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
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

var _ = Describe("VaultSecretStore", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Builder", func() {
		It("fails without logger", func() {
			_, err := NewVaultSecretStore().
				SetAddress("http://localhost:8200").
				SetToken("token").
				SetParentNamespace("osac").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger"))
		})

		It("fails without address", func() {
			_, err := NewVaultSecretStore().
				SetLogger(logger).
				SetToken("token").
				SetParentNamespace("osac").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("address"))
		})

		It("fails without token", func() {
			_, err := NewVaultSecretStore().
				SetLogger(logger).
				SetAddress("http://localhost:8200").
				SetParentNamespace("osac").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("token"))
		})

		It("fails without parent namespace", func() {
			_, err := NewVaultSecretStore().
				SetLogger(logger).
				SetAddress("http://localhost:8200").
				SetToken("token").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("parent namespace"))
		})
	})

	Describe("Store", func() {
		It("writes data to the correct path and namespace", func() {
			var capturedPath string
			var capturedNamespace string
			var capturedToken string
			var capturedBody map[string]any

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				capturedNamespace = r.Header.Get("X-Vault-Namespace")
				capturedToken = r.Header.Get("X-Vault-Token")
				body, _ := io.ReadAll(r.Body)
				json.Unmarshal(body, &capturedBody)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"data":{"created_time":"2026-01-01T00:00:00Z","version":1}}`))
			}))
			defer server.Close()

			store, err := NewVaultSecretStore().
				SetLogger(logger).
				SetAddress(server.URL).
				SetToken("test-token").
				SetParentNamespace("osac").
				Build()
			Expect(err).ToNot(HaveOccurred())

			data := map[string][]byte{
				"password": []byte("secret-value"),
			}
			err = store.Store(ctx, "tenant-a", "my-project", "my-secret", data)
			Expect(err).ToNot(HaveOccurred())

			Expect(capturedPath).To(Equal("/v1/secret/data/my-project/my-secret"))
			Expect(capturedNamespace).To(Equal("osac/tenant-a"))
			Expect(capturedToken).To(Equal("test-token"))
			Expect(capturedBody).To(HaveKey("data"))
			innerData := capturedBody["data"].(map[string]any)
			Expect(innerData["password"]).To(Equal(base64.StdEncoding.EncodeToString([]byte("secret-value"))))
		})

		It("uses name only when project is empty", func() {
			var capturedPath string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"data":{"created_time":"2026-01-01T00:00:00Z","version":1}}`))
			}))
			defer server.Close()

			store, err := NewVaultSecretStore().
				SetLogger(logger).
				SetAddress(server.URL).
				SetToken("test-token").
				SetParentNamespace("osac").
				Build()
			Expect(err).ToNot(HaveOccurred())

			err = store.Store(ctx, "tenant-a", "", "my-secret", map[string][]byte{"key": []byte("val")})
			Expect(err).ToNot(HaveOccurred())

			Expect(capturedPath).To(Equal("/v1/secret/data/my-secret"))
		})

		It("uses custom KV mount path", func() {
			var capturedPath string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"data":{"created_time":"2026-01-01T00:00:00Z","version":1}}`))
			}))
			defer server.Close()

			store, err := NewVaultSecretStore().
				SetLogger(logger).
				SetAddress(server.URL).
				SetToken("test-token").
				SetParentNamespace("osac").
				SetKVMountPath("kv").
				Build()
			Expect(err).ToNot(HaveOccurred())

			err = store.Store(ctx, "tenant-a", "", "my-secret", map[string][]byte{"key": []byte("val")})
			Expect(err).ToNot(HaveOccurred())

			Expect(capturedPath).To(Equal("/v1/kv/data/my-secret"))
		})
	})

	Describe("Fetch", func() {
		It("reads data from the correct path and decodes base64 values", func() {
			encoded := base64.StdEncoding.EncodeToString([]byte("secret-value"))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/v1/secret/data/my-project/my-secret"))
				Expect(r.Header.Get("X-Vault-Namespace")).To(Equal("osac/tenant-a"))
				w.WriteHeader(http.StatusOK)
				resp := map[string]any{
					"data": map[string]any{
						"data": map[string]any{
							"password": encoded,
						},
						"metadata": map[string]any{
							"version": 1,
						},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			store, err := NewVaultSecretStore().
				SetLogger(logger).
				SetAddress(server.URL).
				SetToken("test-token").
				SetParentNamespace("osac").
				Build()
			Expect(err).ToNot(HaveOccurred())

			result, err := store.Fetch(ctx, "tenant-a", "my-project", "my-secret")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveKey("password"))
			Expect(result["password"]).To(Equal([]byte("secret-value")))
		})
	})

	Describe("Delete", func() {
		It("sends delete to the metadata path", func() {
			var capturedPath string
			var capturedMethod string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				capturedMethod = r.Method
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			store, err := NewVaultSecretStore().
				SetLogger(logger).
				SetAddress(server.URL).
				SetToken("test-token").
				SetParentNamespace("osac").
				Build()
			Expect(err).ToNot(HaveOccurred())

			err = store.Delete(ctx, "tenant-a", "my-project", "my-secret")
			Expect(err).ToNot(HaveOccurred())

			Expect(capturedMethod).To(Equal("DELETE"))
			Expect(capturedPath).To(Equal("/v1/secret/metadata/my-project/my-secret"))
		})
	})

	Describe("Error mapping", func() {
		It("maps 403 to PermissionDenied", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"errors":["permission denied"]}`))
			}))
			defer server.Close()

			store, err := NewVaultSecretStore().
				SetLogger(logger).
				SetAddress(server.URL).
				SetToken("test-token").
				SetParentNamespace("osac").
				Build()
			Expect(err).ToNot(HaveOccurred())

			err = store.Store(ctx, "tenant-a", "", "s", map[string][]byte{"k": []byte("v")})
			Expect(err).To(HaveOccurred())
			grpcErr := ToGrpcError(err)
			st, ok := grpcstatus.FromError(grpcErr)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.PermissionDenied))
		})

		It("maps 404 to NotFound", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"errors":[]}`))
			}))
			defer server.Close()

			store, err := NewVaultSecretStore().
				SetLogger(logger).
				SetAddress(server.URL).
				SetToken("test-token").
				SetParentNamespace("osac").
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = store.Fetch(ctx, "tenant-a", "", "nonexistent")
			Expect(err).To(HaveOccurred())
			grpcErr := ToGrpcError(err)
			st, ok := grpcstatus.FromError(grpcErr)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.NotFound))
		})

		It("maps connection errors to Unavailable", func() {
			store, err := NewVaultSecretStore().
				SetLogger(logger).
				SetAddress("http://localhost:1").
				SetToken("test-token").
				SetParentNamespace("osac").
				Build()
			Expect(err).ToNot(HaveOccurred())

			err = store.Store(ctx, "tenant-a", "", "s", map[string][]byte{"k": []byte("v")})
			Expect(err).To(HaveOccurred())
			grpcErr := ToGrpcError(err)
			st, ok := grpcstatus.FromError(grpcErr)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.Unavailable))
		})
	})
})
