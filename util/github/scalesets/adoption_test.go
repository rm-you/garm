// Copyright 2026 Cloudbase Solutions SRL
//
//    Licensed under the Apache License, Version 2.0 (the "License"); you may
//    not use this file except in compliance with the License. You may obtain
//    a copy of the License at
//
//         http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
//    WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
//    License for the specific language governing permissions and limitations
//    under the License.

//go:build testing

package scalesets

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudbase/garm/params"
)

func TestValidateAdoptionLabels(t *testing.T) {
	desired := params.RunnerScaleSet{Labels: []params.Label{{Name: "pool", Type: "System"}, {Name: "linux"}}}
	for _, tt := range []struct {
		name       string
		labels     []params.Label
		compatible bool
	}{
		{"same", desired.Labels, true},
		{"reordered and case folded", []params.Label{{Name: "LINUX", Type: "system"}, {Name: "POOL"}}, true},
		{"duplicate", []params.Label{{Name: "pool"}, {Name: "linux"}, {Name: "Linux"}}, true},
		{"missing", []params.Label{{Name: "pool"}}, false},
		{"extra", []params.Label{{Name: "pool"}, {Name: "linux"}, {Name: "admin"}}, false},
		{"different", []params.Label{{Name: "pool"}, {Name: "windows"}}, false},
		{"omitted", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAdoption(params.RunnerScaleSet{ID: 42, Labels: tt.labels}, desired)
			if tt.compatible {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "different labels")
			}
		})
	}
}
