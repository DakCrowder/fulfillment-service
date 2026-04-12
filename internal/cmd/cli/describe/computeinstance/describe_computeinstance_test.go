/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package computeinstance

import (
	"bytes"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

// formatComputeInstance formats a compute instance for display, matching the logic in the describe command.
func formatComputeInstance(ci *publicv1.ComputeInstance) string {
	var buf bytes.Buffer
	writer := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	template := "-"
	if ci.Spec != nil {
		template = ci.Spec.Template
	}
	state := "-"
	if ci.Status != nil {
		state = ci.Status.State.String()
		state = strings.Replace(state, "COMPUTE_INSTANCE_STATE_", "", -1)
	}
	fmt.Fprintf(writer, "ID:\t%s\n", ci.Id)
	fmt.Fprintf(writer, "Template:\t%s\n", template)
	fmt.Fprintf(writer, "State:\t%s\n", state)
	if ci.Status != nil && ci.Status.GetLastRestartedAt() != nil {
		fmt.Fprintf(writer, "Last Restarted At:\t%s\n", ci.Status.GetLastRestartedAt().AsTime().Format(time.RFC3339))
	}
	writer.Flush()

	return buf.String()
}

var _ = Describe("Describe Compute Instance", func() {
	It("should display last_restarted_at when set", func() {
		restartTime := time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)
		ci := &publicv1.ComputeInstance{
			Id: "ci-test-001",
			Spec: &publicv1.ComputeInstanceSpec{
				Template: "tpl-small-001",
			},
			Status: &publicv1.ComputeInstanceStatus{
				State:           publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
				LastRestartedAt: timestamppb.New(restartTime),
			},
		}

		output := formatComputeInstance(ci)
		Expect(output).To(ContainSubstring("Last Restarted At:"))
		Expect(output).To(ContainSubstring("2026-03-15T10:30:00Z"))
	})

	It("should omit last_restarted_at when not set", func() {
		ci := &publicv1.ComputeInstance{
			Id: "ci-test-002",
			Spec: &publicv1.ComputeInstanceSpec{
				Template: "tpl-small-001",
			},
			Status: &publicv1.ComputeInstanceStatus{
				State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
			},
		}

		output := formatComputeInstance(ci)
		Expect(output).NotTo(ContainSubstring("Last Restarted At:"))
	})

	It("should omit last_restarted_at when status is nil", func() {
		ci := &publicv1.ComputeInstance{
			Id: "ci-test-003",
			Spec: &publicv1.ComputeInstanceSpec{
				Template: "tpl-small-001",
			},
		}

		output := formatComputeInstance(ci)
		Expect(output).To(MatchRegexp(`State:\s+-`))
		Expect(output).NotTo(ContainSubstring("Last Restarted At:"))
	})
})
