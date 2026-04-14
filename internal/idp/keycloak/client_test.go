/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package keycloak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/apiclient"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/idp"
)

var _ = Describe("Keycloak Client", func() {
	var (
		ctx    context.Context
		client *Client
		server *httptest.Server
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	Describe("CreateOrganization", func() {
		It("creates an organization in Keycloak", func() {
			var receivedRealm *keycloakRealm
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/admin/realms" {
					// Create request
					receivedRealm = &keycloakRealm{}
					json.NewDecoder(r.Body).Decode(receivedRealm)
					w.WriteHeader(http.StatusCreated)
					return
				}

				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org" {
					// Get request to verify creation
					enabled := true
					response := keycloakRealm{
						ID:          "realm-uuid-123",
						Realm:       "test-org",
						DisplayName: "Test Organization",
						Enabled:     &enabled,
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(response)
					return
				}

				w.WriteHeader(http.StatusNotFound)
			}))

			client = createTestClient(server.URL)

			org := &idp.Organization{
				Name:        "test-org",
				DisplayName: "Test Organization",
				Enabled:     true,
			}
			createdOrg, err := client.CreateOrganization(ctx, org)
			Expect(err).ToNot(HaveOccurred())
			Expect(receivedRealm.Realm).To(Equal("test-org"))
			Expect(createdOrg).ToNot(BeNil())
			Expect(createdOrg.ID).To(Equal("realm-uuid-123"))
			Expect(createdOrg.Name).To(Equal("test-org"))
			Expect(createdOrg.DisplayName).To(Equal("Test Organization"))
			Expect(createdOrg.Enabled).To(BeTrue())
		})
		It("returns an error if the organization already exists", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
			}))

			client = createTestClient(server.URL)

			org := &idp.Organization{
				Name:        "test-org",
				DisplayName: "Test Organization",
				Enabled:     true,
			}
			_, err := client.CreateOrganization(ctx, org)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already exists"))
			Expect(err.Error()).To(ContainSubstring("test-org"))

			var apiErr *apiclient.APIError
			Expect(errors.As(err, &apiErr)).To(BeTrue())
			Expect(apiErr.StatusCode).To(Equal(http.StatusConflict))
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)

			org := &idp.Organization{Name: "test-org"}
			_, err := client.CreateOrganization(ctx, org)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("GetOrganization", func() {
		It("retrieves an organization from Keycloak", func() {
			enabled := true
			testRealm := &keycloakRealm{
				ID:          "org-id",
				Realm:       "test-org",
				DisplayName: "Test Organization",
				Enabled:     &enabled,
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(testRealm)
			}))

			client = createTestClient(server.URL)

			org, err := client.GetOrganization(ctx, "test-org")
			Expect(err).ToNot(HaveOccurred())
			Expect(org.Name).To(Equal("test-org"))
			Expect(org.DisplayName).To(Equal("Test Organization"))
		})
		It("returns an error if the organization is not found", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))

			client = createTestClient(server.URL)
			_, err := client.GetOrganization(ctx, "test-org")
			Expect(err).To(HaveOccurred())
		})

		It("returns an error on malformed JSON response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("invalid json"))
			}))

			client = createTestClient(server.URL)
			_, err := client.GetOrganization(ctx, "test-org")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to decode organization response"))
		})
	})

	Describe("CreateUser", func() {
		It("creates a user and extracts ID from Location header", func() {
			var receivedUser *keycloakUser
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedUser = &keycloakUser{}
				json.NewDecoder(r.Body).Decode(receivedUser)
				w.Header().Set("Location", "/admin/realms/test-org/users/user-123-abc")
				w.WriteHeader(http.StatusCreated)
			}))

			client = createTestClient(server.URL)

			user := &idp.User{
				Username:      "testuser",
				Email:         "test@example.com",
				EmailVerified: true,
				Enabled:       true,
				FirstName:     "Test",
				LastName:      "User",
			}
			createdUser, err := client.CreateUser(ctx, "test-org", user)
			Expect(err).ToNot(HaveOccurred())
			Expect(receivedUser.Username).To(Equal("testuser"))
			Expect(createdUser).ToNot(BeNil())
			Expect(createdUser.ID).To(Equal("user-123-abc"))
			Expect(createdUser.Username).To(Equal("testuser"))
			Expect(createdUser.Email).To(Equal("test@example.com"))
		})
		It("returns an error if the Location header is not present", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
			}))

			client = createTestClient(server.URL)
			_, err := client.CreateUser(ctx, "test-org", &idp.User{
				Username: "testuser",
				Email:    "test@example.com",
				Enabled:  true,
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Location header not present in create user response"))
		})

		It("returns an error if the user already exists", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
			}))

			client = createTestClient(server.URL)
			_, err := client.CreateUser(ctx, "test-org", &idp.User{
				Username: "testuser",
				Email:    "test@example.com",
				Enabled:  true,
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already exists"))

			var apiErr *apiclient.APIError
			Expect(errors.As(err, &apiErr)).To(BeTrue())
			Expect(apiErr.StatusCode).To(Equal(http.StatusConflict))
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			_, err := client.CreateUser(ctx, "test-org", &idp.User{
				Username: "testuser",
				Email:    "test@example.com",
				Enabled:  true,
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create user"))
		})
	})

	Describe("ListUsers", func() {
		It("fetches all users across multiple pages", func() {
			requestCount := 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))

				// Parse query parameters
				query := r.URL.Query()
				first := query.Get("first")
				max := query.Get("max")

				requestCount++

				// Simulate pagination: first page returns 100 users, second page returns 50
				var users []keycloakUser
				if first == "0" && max == "100" {
					// First page: return 100 users
					for i := 0; i < 100; i++ {
						enabled := true
						users = append(users, keycloakUser{
							ID:       fmt.Sprintf("user-%d", i),
							Username: fmt.Sprintf("user%d", i),
							Enabled:  &enabled,
						})
					}
				} else if first == "100" && max == "100" {
					// Second page: return 50 users (less than max, indicates last page)
					for i := 100; i < 150; i++ {
						enabled := true
						users = append(users, keycloakUser{
							ID:       fmt.Sprintf("user-%d", i),
							Username: fmt.Sprintf("user%d", i),
							Enabled:  &enabled,
						})
					}
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(users)
			}))

			client = createTestClient(server.URL)

			users, err := client.ListUsers(ctx, "test-org")
			Expect(err).ToNot(HaveOccurred())

			// Should have fetched all 150 users across 2 pages
			Expect(users).To(HaveLen(150))

			// Should have made 2 requests (one per page)
			Expect(requestCount).To(Equal(2))

			// Verify first and last user
			Expect(users[0].ID).To(Equal("user-0"))
			Expect(users[149].ID).To(Equal("user-149"))
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			_, err := client.ListUsers(ctx, "test-org")
			Expect(err).To(HaveOccurred())
		})

		It("returns an error on malformed JSON response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("invalid json"))
			}))

			client = createTestClient(server.URL)
			_, err := client.ListUsers(ctx, "test-org")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to decode users"))
		})

		It("respects context cancellation during pagination", func() {
			requestCount := 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++

				// Return a full page so pagination continues
				var users []keycloakUser
				for i := 0; i < 100; i++ {
					enabled := true
					users = append(users, keycloakUser{
						ID:       fmt.Sprintf("user-%d", i),
						Username: fmt.Sprintf("user%d", i),
						Enabled:  &enabled,
					})
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(users)
			}))

			client = createTestClient(server.URL)

			// Create a context that is already cancelled
			cancelledCtx, cancel := context.WithCancel(context.Background())
			cancel()

			_, err := client.ListUsers(cancelledCtx, "test-org")
			Expect(err).To(HaveOccurred())
			Expect(err).To(Equal(context.Canceled))

			// Should not have made any requests since context was already cancelled
			Expect(requestCount).To(Equal(0))
		})
	})
	Describe("GetUser", func() {
		It("gets a user from Keycloak", func() {
			enabled := true
			testUser := &keycloakUser{
				ID:            "user-123-abc",
				Username:      "testuser",
				Email:         "test@example.com",
				EmailVerified: &enabled,
				Enabled:       &enabled,
				FirstName:     "Test",
				LastName:      "User",
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/users/user-123-abc"))

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(testUser)
			}))
			client = createTestClient(server.URL)
			user, err := client.GetUser(ctx, "test-org", "user-123-abc")
			Expect(err).ToNot(HaveOccurred())
			Expect(user.ID).To(Equal("user-123-abc"))
			Expect(user.Username).To(Equal("testuser"))
			Expect(user.Email).To(Equal("test@example.com"))
			Expect(user.Enabled).To(BeTrue())
			Expect(user.FirstName).To(Equal("Test"))
			Expect(user.LastName).To(Equal("User"))
			Expect(user.Attributes).To(BeNil())
			Expect(user.Groups).To(BeNil())
			Expect(user.Credentials).To(BeNil())
			Expect(user.RequiredActions).To(BeNil())
		})
		It("returns an error if the user is not found", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))

			client = createTestClient(server.URL)
			_, err := client.GetUser(ctx, "test-org", "user-123-abc")
			Expect(err).To(HaveOccurred())
		})

		It("returns an error on malformed JSON response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("invalid json"))
			}))

			client = createTestClient(server.URL)
			_, err := client.GetUser(ctx, "test-org", "user-123-abc")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to decode user response"))
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			_, err := client.GetUser(ctx, "test-org", "user-123-abc")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("DeleteUser", func() {
		It("deletes a user from Keycloak", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodDelete))
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/users/user-123-abc"))
				w.WriteHeader(http.StatusNoContent)
			}))
			client = createTestClient(server.URL)
			err := client.DeleteUser(ctx, "test-org", "user-123-abc")
			Expect(err).ToNot(HaveOccurred())
		})
		It("returns an error if the user is not found", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))

			client = createTestClient(server.URL)
			err := client.DeleteUser(ctx, "test-org", "user-123-abc")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
			Expect(err.Error()).To(ContainSubstring("user-123-abc"))

			var apiErr *apiclient.APIError
			Expect(errors.As(err, &apiErr)).To(BeTrue())
			Expect(apiErr.StatusCode).To(Equal(http.StatusNotFound))
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			err := client.DeleteUser(ctx, "test-org", "user-123-abc")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("DeleteOrganization", func() {
		It("deletes an organization from Keycloak", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodDelete))
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org"))
				w.WriteHeader(http.StatusNoContent)
			}))

			client = createTestClient(server.URL)
			err := client.DeleteOrganization(ctx, "test-org")
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns an error if the organization is not found", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))

			client = createTestClient(server.URL)
			err := client.DeleteOrganization(ctx, "test-org")
			Expect(err).To(HaveOccurred())
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			err := client.DeleteOrganization(ctx, "test-org")
			Expect(err).To(HaveOccurred())
		})

		It("clears cached realm-management UUID when organization is deleted", func() {
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			requestCount := 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++

				// Handle GetClientByClientID requests
				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org/clients" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(clients)
					return
				}

				// Handle DeleteOrganization request
				if r.Method == http.MethodDelete && r.URL.Path == "/admin/realms/test-org" {
					w.WriteHeader(http.StatusNoContent)
					return
				}

				w.WriteHeader(http.StatusNotFound)
			}))

			client = createTestClient(server.URL)

			// First, populate the cache by calling GetClientByClientID
			internalID, err := client.GetClientByClientID(ctx, "test-org", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(internalID).To(Equal("internal-uuid-123"))
			Expect(requestCount).To(Equal(1))

			// Verify the cache is populated (second call doesn't make a request)
			internalID2, err := client.GetClientByClientID(ctx, "test-org", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(internalID2).To(Equal("internal-uuid-123"))
			Expect(requestCount).To(Equal(1)) // Still 1, used cache

			// Delete the organization
			err = client.DeleteOrganization(ctx, "test-org")
			Expect(err).ToNot(HaveOccurred())
			Expect(requestCount).To(Equal(2)) // Delete request made

			// Verify the cache was cleared (next call makes a new request)
			internalID3, err := client.GetClientByClientID(ctx, "test-org", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(internalID3).To(Equal("internal-uuid-123"))
			Expect(requestCount).To(Equal(3)) // New request made, cache was cleared
		})
	})

	Describe("ListOrganizationRoles", func() {
		It("fetches organization-level roles", func() {
			clientRole := false
			roles := []keycloakRole{
				{ID: "role1", Name: "admin", ClientRole: &clientRole},
				{ID: "role2", Name: "user", ClientRole: &clientRole},
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/roles"))

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(roles)
			}))

			client = createTestClient(server.URL)
			result, err := client.ListOrganizationRoles(ctx, "test-org")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveLen(2))
			Expect(result[0].Name).To(Equal("admin"))
			Expect(result[1].Name).To(Equal("user"))
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			_, err := client.ListOrganizationRoles(ctx, "test-org")
			Expect(err).To(HaveOccurred())
		})

		It("returns an error on malformed JSON response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("invalid json"))
			}))

			client = createTestClient(server.URL)
			_, err := client.ListOrganizationRoles(ctx, "test-org")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to decode realm roles"))
		})
	})

	Describe("ListClientRoles", func() {
		It("fetches client-level roles", func() {
			clientRole := true
			roles := []keycloakRole{
				{ID: "role1", Name: "manage-users", ClientRole: &clientRole},
				{ID: "role2", Name: "view-users", ClientRole: &clientRole},
			}
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))

				// First call is to resolve client ID
				if r.URL.Path == "/admin/realms/test-org/clients" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(clients)
					return
				}

				// Second call is to fetch roles
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/clients/internal-uuid-123/roles"))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(roles)
			}))

			client = createTestClient(server.URL)
			result, err := client.ListClientRoles(ctx, "test-org", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveLen(2))
			Expect(result[0].Name).To(Equal("manage-users"))
			Expect(result[1].Name).To(Equal("view-users"))
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			_, err := client.ListClientRoles(ctx, "test-org", "realm-management")
			Expect(err).To(HaveOccurred())
		})

		It("returns an error on malformed JSON response when fetching roles", func() {
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)

				// First call returns valid clients, second call returns invalid JSON
				if r.URL.Path == "/admin/realms/test-org/clients" {
					json.NewEncoder(w).Encode(clients)
				} else {
					w.Write([]byte("invalid json"))
				}
			}))

			client = createTestClient(server.URL)
			_, err := client.ListClientRoles(ctx, "test-org", "realm-management")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to decode client roles"))
		})
	})

	Describe("AssignOrganizationRolesToUser", func() {
		It("assigns organization roles to a user", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodPost))
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/users/user-123/role-mappings/realm"))
				w.WriteHeader(http.StatusNoContent)
			}))

			client = createTestClient(server.URL)
			roles := []*idp.Role{
				{ID: "role1", Name: "admin"},
			}
			err := client.AssignOrganizationRolesToUser(ctx, "test-org", "user-123", roles)
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			roles := []*idp.Role{{ID: "role1", Name: "admin"}}
			err := client.AssignOrganizationRolesToUser(ctx, "test-org", "user-123", roles)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("AssignClientRolesToUser", func() {
		It("assigns client roles to a user", func() {
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// First call is to resolve client ID
				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org/clients" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(clients)
					return
				}

				// Second call is to assign roles
				Expect(r.Method).To(Equal(http.MethodPost))
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/users/user-123/role-mappings/clients/internal-uuid-123"))
				w.WriteHeader(http.StatusNoContent)
			}))

			client = createTestClient(server.URL)
			roles := []*idp.Role{
				{ID: "role1", Name: "manage-users"},
			}
			err := client.AssignClientRolesToUser(ctx, "test-org", "user-123", "realm-management", roles)
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			roles := []*idp.Role{{ID: "role1", Name: "manage-users"}}
			err := client.AssignClientRolesToUser(ctx, "test-org", "user-123", "realm-management", roles)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("RemoveOrganizationRolesFromUser", func() {
		It("removes organization roles from a user", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodDelete))
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/users/user-123/role-mappings/realm"))
				w.WriteHeader(http.StatusNoContent)
			}))

			client = createTestClient(server.URL)
			roles := []*idp.Role{
				{ID: "role1", Name: "admin"},
			}
			err := client.RemoveOrganizationRolesFromUser(ctx, "test-org", "user-123", roles)
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			roles := []*idp.Role{{ID: "role1", Name: "admin"}}
			err := client.RemoveOrganizationRolesFromUser(ctx, "test-org", "user-123", roles)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("RemoveClientRolesFromUser", func() {
		It("removes client roles from a user", func() {
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// First call is to resolve client ID
				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org/clients" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(clients)
					return
				}

				// Second call is to remove roles
				Expect(r.Method).To(Equal(http.MethodDelete))
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/users/user-123/role-mappings/clients/internal-uuid-123"))
				w.WriteHeader(http.StatusNoContent)
			}))

			client = createTestClient(server.URL)
			roles := []*idp.Role{
				{ID: "role1", Name: "manage-users"},
			}
			err := client.RemoveClientRolesFromUser(ctx, "test-org", "user-123", "realm-management", roles)
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			roles := []*idp.Role{{ID: "role1", Name: "manage-users"}}
			err := client.RemoveClientRolesFromUser(ctx, "test-org", "user-123", "realm-management", roles)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("GetUserOrganizationRoles", func() {
		It("gets organization roles assigned to a user", func() {
			clientRole := false
			roles := []keycloakRole{
				{ID: "role1", Name: "admin", ClientRole: &clientRole},
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/users/user-123/role-mappings/realm"))

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(roles)
			}))

			client = createTestClient(server.URL)
			result, err := client.GetUserOrganizationRoles(ctx, "test-org", "user-123")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveLen(1))
			Expect(result[0].Name).To(Equal("admin"))
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			_, err := client.GetUserOrganizationRoles(ctx, "test-org", "user-123")
			Expect(err).To(HaveOccurred())
		})

		It("returns an error on malformed JSON response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("invalid json"))
			}))

			client = createTestClient(server.URL)
			_, err := client.GetUserOrganizationRoles(ctx, "test-org", "user-123")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to decode user realm roles"))
		})
	})

	Describe("GetUserClientRoles", func() {
		It("gets client roles assigned to a user", func() {
			clientRole := true
			roles := []keycloakRole{
				{ID: "role1", Name: "manage-users", ClientRole: &clientRole},
			}
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))

				// First call is to resolve client ID
				if r.URL.Path == "/admin/realms/test-org/clients" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(clients)
					return
				}

				// Second call is to fetch user's client roles
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/users/user-123/role-mappings/clients/internal-uuid-123"))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(roles)
			}))

			client = createTestClient(server.URL)
			result, err := client.GetUserClientRoles(ctx, "test-org", "user-123", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveLen(1))
			Expect(result[0].Name).To(Equal("manage-users"))
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			_, err := client.GetUserClientRoles(ctx, "test-org", "user-123", "realm-management")
			Expect(err).To(HaveOccurred())
		})

		It("returns an error on malformed JSON response when fetching roles", func() {
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)

				// First call returns valid clients, second call returns invalid JSON
				if r.URL.Path == "/admin/realms/test-org/clients" {
					json.NewEncoder(w).Encode(clients)
				} else {
					w.Write([]byte("invalid json"))
				}
			}))

			client = createTestClient(server.URL)
			_, err := client.GetUserClientRoles(ctx, "test-org", "user-123", "realm-management")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to decode user client roles"))
		})
	})

	Describe("AssignOrganizationAdminPermissions", func() {
		It("assigns all realm management roles to a user", func() {
			clientRole := true
			// Return a mix of roles - some that match keycloakRealmManagementRoles and some that don't
			allRoles := []keycloakRole{
				{ID: "role1", Name: "manage-realm", ClientRole: &clientRole},
				{ID: "role2", Name: "manage-users", ClientRole: &clientRole},
				{ID: "role3", Name: "manage-clients", ClientRole: &clientRole},
				{ID: "role4", Name: "some-other-role", ClientRole: &clientRole}, // Should be filtered out
				{ID: "role5", Name: "view-realm", ClientRole: &clientRole},
			}
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			var assignedRoles []keycloakRole
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				// Handle different request types based on method and path
				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org/clients" {
					// Resolve client ID (cached after first call)
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(clients)
					return
				}

				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org/clients/internal-uuid-123/roles" {
					// List client roles
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(allRoles)
					return
				}

				if r.Method == http.MethodPost && r.URL.Path == "/admin/realms/test-org/users/user-123/role-mappings/clients/internal-uuid-123" {
					// Assign client roles
					json.NewDecoder(r.Body).Decode(&assignedRoles)
					w.WriteHeader(http.StatusNoContent)
					return
				}

				// Unexpected request
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			err := client.AssignOrganizationAdminPermissions(ctx, "test-org", "user-123")
			Expect(err).ToNot(HaveOccurred())

			// Verify that only the keycloakRealmManagementRoles were assigned
			Expect(assignedRoles).ToNot(BeEmpty())
			roleNames := make([]string, len(assignedRoles))
			for i, role := range assignedRoles {
				roleNames[i] = role.Name
			}
			Expect(roleNames).To(ContainElement("manage-realm"))
			Expect(roleNames).To(ContainElement("manage-users"))
			Expect(roleNames).To(ContainElement("manage-clients"))
			Expect(roleNames).To(ContainElement("view-realm"))
			Expect(roleNames).ToNot(ContainElement("some-other-role"))
		})

		It("returns an error when ListClientRoles fails", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			err := client.AssignOrganizationAdminPermissions(ctx, "test-org", "user-123")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to list realm-management roles"))
		})

		It("returns an error when no matching roles are found", func() {
			clientRole := true
			// Return roles that don't match keycloakRealmManagementRoles
			allRoles := []keycloakRole{
				{ID: "role1", Name: "some-random-role", ClientRole: &clientRole},
				{ID: "role2", Name: "another-role", ClientRole: &clientRole},
			}
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			requestCount := 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)

				if requestCount == 1 {
					json.NewEncoder(w).Encode(clients)
				} else {
					json.NewEncoder(w).Encode(allRoles)
				}
			}))

			client = createTestClient(server.URL)
			err := client.AssignOrganizationAdminPermissions(ctx, "test-org", "user-123")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no realm management roles found"))
		})

		It("returns an error when AssignClientRolesToUser fails", func() {
			clientRole := true
			allRoles := []keycloakRole{
				{ID: "role1", Name: "manage-realm", ClientRole: &clientRole},
			}
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				// Resolve client ID succeeds
				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org/clients" {
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(clients)
					return
				}

				// List roles succeeds
				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org/clients/internal-uuid-123/roles" {
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(allRoles)
					return
				}

				// Assign roles fails
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			err := client.AssignOrganizationAdminPermissions(ctx, "test-org", "user-123")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to assign realm management roles"))
		})
	})

	Describe("AssignIdpManagerPermissions", func() {
		It("assigns limited IdP manager roles to a user", func() {
			clientRole := true
			// Return a mix of roles - some that match keycloakIdpManagerRoles and some that don't
			allRoles := []keycloakRole{
				{ID: "role1", Name: "manage-users", ClientRole: &clientRole},
				{ID: "role2", Name: "view-users", ClientRole: &clientRole},
				{ID: "role3", Name: "manage-identity-providers", ClientRole: &clientRole},
				{ID: "role4", Name: "view-identity-providers", ClientRole: &clientRole},
				{ID: "role5", Name: "view-realm", ClientRole: &clientRole},
				{ID: "role6", Name: "manage-realm", ClientRole: &clientRole},   // Should be filtered out
				{ID: "role7", Name: "manage-clients", ClientRole: &clientRole}, // Should be filtered out
			}
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			var assignedRoles []keycloakRole
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				// Handle different request types based on method and path
				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org/clients" {
					// Resolve client ID (cached after first call)
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(clients)
					return
				}

				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org/clients/internal-uuid-123/roles" {
					// List client roles
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(allRoles)
					return
				}

				if r.Method == http.MethodPost && r.URL.Path == "/admin/realms/test-org/users/user-123/role-mappings/clients/internal-uuid-123" {
					// Assign client roles
					json.NewDecoder(r.Body).Decode(&assignedRoles)
					w.WriteHeader(http.StatusNoContent)
					return
				}

				// Unexpected request
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			err := client.AssignIdpManagerPermissions(ctx, "test-org", "user-123")
			Expect(err).ToNot(HaveOccurred())

			// Verify that only the keycloakIdpManagerRoles were assigned
			Expect(assignedRoles).ToNot(BeEmpty())
			roleNames := make([]string, len(assignedRoles))
			for i, role := range assignedRoles {
				roleNames[i] = role.Name
			}
			Expect(roleNames).To(ContainElement("manage-users"))
			Expect(roleNames).To(ContainElement("view-users"))
			Expect(roleNames).To(ContainElement("manage-identity-providers"))
			Expect(roleNames).To(ContainElement("view-identity-providers"))
			Expect(roleNames).To(ContainElement("view-realm"))
			Expect(roleNames).ToNot(ContainElement("manage-realm"))
			Expect(roleNames).ToNot(ContainElement("manage-clients"))
		})

		It("returns an error when ListClientRoles fails", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			err := client.AssignIdpManagerPermissions(ctx, "test-org", "user-123")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to list realm-management roles"))
		})

		It("returns an error when no matching roles are found", func() {
			clientRole := true
			// Return roles that don't match keycloakIdpManagerRoles
			allRoles := []keycloakRole{
				{ID: "role1", Name: "some-random-role", ClientRole: &clientRole},
				{ID: "role2", Name: "another-role", ClientRole: &clientRole},
			}
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			requestCount := 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)

				if requestCount == 1 {
					json.NewEncoder(w).Encode(clients)
				} else {
					json.NewEncoder(w).Encode(allRoles)
				}
			}))

			client = createTestClient(server.URL)
			err := client.AssignIdpManagerPermissions(ctx, "test-org", "user-123")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no IdP manager roles found"))
		})

		It("returns an error when AssignClientRolesToUser fails", func() {
			clientRole := true
			allRoles := []keycloakRole{
				{ID: "role1", Name: "manage-users", ClientRole: &clientRole},
			}
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				// Resolve client ID succeeds
				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org/clients" {
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(clients)
					return
				}

				// List roles succeeds
				if r.Method == http.MethodGet && r.URL.Path == "/admin/realms/test-org/clients/internal-uuid-123/roles" {
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(allRoles)
					return
				}

				// Assign roles fails
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			err := client.AssignIdpManagerPermissions(ctx, "test-org", "user-123")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to assign IdP manager roles"))
		})
	})

	Describe("GetClientByClientID", func() {
		It("resolves client ID from clientId attribute", func() {
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/clients"))
				Expect(r.URL.Query().Get("clientId")).To(Equal("realm-management"))

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(clients)
			}))

			client = createTestClient(server.URL)
			internalID, err := client.GetClientByClientID(ctx, "test-org", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(internalID).To(Equal("internal-uuid-123"))
		})

		It("returns valid UUID immediately without making API call", func() {
			// Create a server that will fail if called
			serverCalled := false
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				serverCalled = true
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			validUUID := "550e8400-e29b-41d4-a716-446655440000"
			internalID, err := client.GetClientByClientID(ctx, "test-org", validUUID)
			Expect(err).ToNot(HaveOccurred())
			Expect(internalID).To(Equal(validUUID))

			// Verify no HTTP call was made
			Expect(serverCalled).To(BeFalse())
		})

		It("returns an error when clientId is not found", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode([]keycloakClient{})
			}))

			client = createTestClient(server.URL)
			_, err := client.GetClientByClientID(ctx, "test-org", "not-a-valid-uuid")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("returns an error on server error", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			_, err := client.GetClientByClientID(ctx, "test-org", "realm-management")
			Expect(err).To(HaveOccurred())
		})

		It("returns an error on malformed JSON response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("invalid json"))
			}))

			client = createTestClient(server.URL)
			_, err := client.GetClientByClientID(ctx, "test-org", "realm-management")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to decode clients response"))
		})

		It("stores realm-management UUID to avoid repeated API calls", func() {
			clients := []keycloakClient{
				{ID: "internal-uuid-123", ClientID: "realm-management"},
			}

			requestCount := 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				Expect(r.Method).To(Equal(http.MethodGet))
				Expect(r.URL.Path).To(Equal("/admin/realms/test-org/clients"))

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(clients)
			}))

			client = createTestClient(server.URL)

			// First call - makes HTTP request
			internalID1, err := client.GetClientByClientID(ctx, "test-org", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(internalID1).To(Equal("internal-uuid-123"))
			Expect(requestCount).To(Equal(1))

			// Second call - uses stored UUID, no HTTP request
			internalID2, err := client.GetClientByClientID(ctx, "test-org", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(internalID2).To(Equal("internal-uuid-123"))
			Expect(requestCount).To(Equal(1)) // Still 1, not incremented

			// Third call - uses stored UUID
			internalID3, err := client.GetClientByClientID(ctx, "test-org", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(internalID3).To(Equal("internal-uuid-123"))
			Expect(requestCount).To(Equal(1))
		})

		It("stores realm-management UUIDs for multiple organizations", func() {
			requestCount := 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				Expect(r.Method).To(Equal(http.MethodGet))

				// Return different UUIDs based on organization
				var clients []keycloakClient
				if r.URL.Path == "/admin/realms/org-1/clients" {
					clients = []keycloakClient{{ID: "uuid-for-org-1", ClientID: "realm-management"}}
				} else if r.URL.Path == "/admin/realms/org-2/clients" {
					clients = []keycloakClient{{ID: "uuid-for-org-2", ClientID: "realm-management"}}
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(clients)
			}))

			client = createTestClient(server.URL)

			// First org - makes HTTP request
			id1, err := client.GetClientByClientID(ctx, "org-1", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(id1).To(Equal("uuid-for-org-1"))
			Expect(requestCount).To(Equal(1))

			// Second org - makes HTTP request
			id2, err := client.GetClientByClientID(ctx, "org-2", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(id2).To(Equal("uuid-for-org-2"))
			Expect(requestCount).To(Equal(2))

			// First org again - uses stored UUID
			id1Again, err := client.GetClientByClientID(ctx, "org-1", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(id1Again).To(Equal("uuid-for-org-1"))
			Expect(requestCount).To(Equal(2)) // No new request

			// Second org again - uses stored UUID
			id2Again, err := client.GetClientByClientID(ctx, "org-2", "realm-management")
			Expect(err).ToNot(HaveOccurred())
			Expect(id2Again).To(Equal("uuid-for-org-2"))
			Expect(requestCount).To(Equal(2)) // No new request
		})

		It("does not store UUIDs passed directly", func() {
			serverCalled := false
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				serverCalled = true
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = createTestClient(server.URL)
			validUUID := "550e8400-e29b-41d4-a716-446655440000"

			// First call with UUID
			id1, err := client.GetClientByClientID(ctx, "test-org", validUUID)
			Expect(err).ToNot(HaveOccurred())
			Expect(id1).To(Equal(validUUID))
			Expect(serverCalled).To(BeFalse())

			// Second call with same UUID
			id2, err := client.GetClientByClientID(ctx, "test-org", validUUID)
			Expect(err).ToNot(HaveOccurred())
			Expect(id2).To(Equal(validUUID))
			Expect(serverCalled).To(BeFalse()) // Still no HTTP call
		})
	})
})

func createTestClient(serverURL string) *Client {
	tokenSource, err := auth.NewStaticTokenSource().
		SetLogger(logger).
		SetToken(&auth.Token{Access: "test-token"}).
		Build()
	Expect(err).ToNot(HaveOccurred())

	client, err := NewClient().
		SetLogger(logger).
		SetBaseURL(serverURL).
		SetTokenSource(tokenSource).
		Build()
	Expect(err).ToNot(HaveOccurred())

	return client
}
