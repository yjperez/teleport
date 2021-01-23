/*
Copyright 2015-2020 Gravitational, Inc.

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

package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gravitational/teleport"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/api/types/wrappers"
	"github.com/gravitational/teleport/lib/defaults"
	"github.com/gravitational/teleport/lib/fixtures"
	"github.com/gravitational/teleport/lib/tlsca"
	"github.com/gravitational/teleport/lib/utils"

	"github.com/gravitational/trace"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/require"
)

// TestConnAndSessLimits verifies that role sets correctly calculate
// a user's MaxConnections and MaxSessions values from multiple
// roles with different individual values.  These are tested together since
// both values use the same resolution rules.
func TestConnAndSessLimits(t *testing.T) {
	utils.InitLoggerForTests(testing.Verbose())
	tts := []struct {
		desc string
		vals []int64
		want int64
	}{
		{
			desc: "smallest nonzero value is selected from mixed values",
			vals: []int64{8, 6, 7, 5, 3, 0, 9},
			want: 3,
		},
		{
			desc: "smallest value selected from all nonzero values",
			vals: []int64{5, 6, 7, 8},
			want: 5,
		},
		{
			desc: "all zero values results in a zero value",
			vals: []int64{0, 0, 0, 0, 0, 0, 0},
			want: 0,
		},
	}
	for ti, tt := range tts {
		cmt := fmt.Sprintf("test case %d: %s", ti, tt.desc)
		var set RoleSet
		for i, val := range tt.vals {
			role := &RoleV3{
				Kind:    KindRole,
				Version: V3,
				Metadata: Metadata{
					Name:      fmt.Sprintf("role-%d", i),
					Namespace: defaults.Namespace,
				},
				Spec: RoleSpecV3{
					Options: RoleOptions{
						MaxConnections: val,
						MaxSessions:    val,
					},
				},
			}
			require.NoError(t, role.CheckAndSetDefaults(), cmt)
			set = append(set, role)
		}
		require.Equal(t, tt.want, set.MaxConnections(), cmt)
		require.Equal(t, tt.want, set.MaxSessions(), cmt)
	}
}

func TestRoleParse(t *testing.T) {
	utils.InitLoggerForTests(testing.Verbose())
	testCases := []struct {
		name         string
		in           string
		role         RoleV3
		error        error
		matchMessage string
	}{
		{
			name:  "no input, should not parse",
			in:    ``,
			role:  RoleV3{},
			error: trace.BadParameter("empty input"),
		},
		{
			name:  "validation error, no name",
			in:    `{}`,
			role:  RoleV3{},
			error: trace.BadParameter("failed to validate: name: name is required"),
		},
		{
			name:  "validation error, no name",
			in:    `{"kind": "role"}`,
			role:  RoleV3{},
			error: trace.BadParameter("failed to validate: name: name is required"),
		},

		{
			name: "validation error, missing resources",
			in: `{
							   		      "kind": "role",
							   		      "version": "v3",
							   		      "metadata": {"name": "name1"},
							   		      "spec": {
							                    "allow": {
							                      "node_labels": {"a": "b"},
							                      "namespaces": ["default"],
							                      "rules": [
							                        {
							                          "verbs": ["read", "list"]
							                        }
							                      ]
							                    }
							   		      }
							   		    }`,
			error:        trace.BadParameter(""),
			matchMessage: "missing resources",
		},
		{
			name: "validation error, missing verbs",
			in: `{
							   		      "kind": "role",
							   		      "version": "v3",
							   		      "metadata": {"name": "name1"},
							   		      "spec": {
							                    "allow": {
							                      "node_labels": {"a": "b"},
							                      "namespaces": ["default"],
							                      "rules": [
							                        {
							                          "resources": ["role"]
							                        }
							                      ]
							                    }
							   		      }
							   		    }`,
			error:        trace.BadParameter(""),
			matchMessage: "missing verbs",
		},
		{
			name: "role with no spec still gets defaults",
			in:   `{"kind": "role", "version": "v3", "metadata": {"name": "defrole"}, "spec": {}}`,
			role: RoleV3{
				Kind:    KindRole,
				Version: V3,
				Metadata: Metadata{
					Name:      "defrole",
					Namespace: defaults.Namespace,
				},
				Spec: RoleSpecV3{
					Options: RoleOptions{
						CertificateFormat: teleport.CertificateFormatStandard,
						MaxSessionTTL:     NewDuration(defaults.MaxCertDuration),
						PortForwarding:    NewBoolOption(true),
						BPF:               defaults.EnhancedEvents(),
					},
					Allow: RoleConditions{
						NodeLabels:       Labels{},
						AppLabels:        Labels{Wildcard: []string{Wildcard}},
						KubernetesLabels: Labels{Wildcard: []string{Wildcard}},
						DatabaseLabels:   Labels{Wildcard: []string{Wildcard}},
						Namespaces:       []string{defaults.Namespace},
					},
					Deny: RoleConditions{
						Namespaces: []string{defaults.Namespace},
					},
				},
			},
			error: nil,
		},
		{
			name: "full valid role",
			in: `{
					   		      "kind": "role",
					   		      "version": "v3",
					   		      "metadata": {"name": "name1", "labels": {"a-b": "c"}},
					   		      "spec": {
					                    "options": {
					                      "cert_format": "standard",
					                      "max_session_ttl": "20h",
					                      "port_forwarding": true,
					                      "client_idle_timeout": "17m",
					                      "disconnect_expired_cert": "yes",
			                              "enhanced_recording": ["command", "network"]
					                    },
					                    "allow": {
					                      "node_labels": {"a": "b", "c-d": "e"},
					                      "app_labels": {"a": "b", "c-d": "e"},
					                      "kubernetes_labels": {"a": "b", "c-d": "e"},
										  "db_labels": {"a": "b", "c-d": "e"},
										  "db_names": ["postgres"],
										  "db_users": ["postgres"],
					                      "namespaces": ["default"],
					                      "rules": [
					                        {
					                          "resources": ["role"],
					                          "verbs": ["read", "list"],
					                          "where": "contains(user.spec.traits[\"groups\"], \"prod\")",
					                          "actions": [
					                             "log(\"info\", \"log entry\")"
					                          ]
					                        }
					                      ]
					                    },
					                    "deny": {
					                      "logins": ["c"]
					                    }
					   		      }
					   		    }`,
			role: RoleV3{
				Kind:    KindRole,
				Version: V3,
				Metadata: Metadata{
					Name:      "name1",
					Namespace: defaults.Namespace,
					Labels:    map[string]string{"a-b": "c"},
				},
				Spec: RoleSpecV3{
					Options: RoleOptions{
						CertificateFormat:     teleport.CertificateFormatStandard,
						MaxSessionTTL:         NewDuration(20 * time.Hour),
						PortForwarding:        NewBoolOption(true),
						ClientIdleTimeout:     NewDuration(17 * time.Minute),
						DisconnectExpiredCert: NewBool(true),
						BPF:                   defaults.EnhancedEvents(),
					},
					Allow: RoleConditions{
						NodeLabels:       Labels{"a": []string{"b"}, "c-d": []string{"e"}},
						AppLabels:        Labels{"a": []string{"b"}, "c-d": []string{"e"}},
						KubernetesLabels: Labels{"a": []string{"b"}, "c-d": []string{"e"}},
						DatabaseLabels:   Labels{"a": []string{"b"}, "c-d": []string{"e"}},
						DatabaseNames:    []string{"postgres"},
						DatabaseUsers:    []string{"postgres"},
						Namespaces:       []string{"default"},
						Rules: []Rule{
							{
								Resources: []string{KindRole},
								Verbs:     []string{VerbRead, VerbList},
								Where:     "contains(user.spec.traits[\"groups\"], \"prod\")",
								Actions: []string{
									"log(\"info\", \"log entry\")",
								},
							},
						},
					},
					Deny: RoleConditions{
						Namespaces: []string{defaults.Namespace},
						Logins:     []string{"c"},
					},
				},
			},
			error: nil,
		},
		{
			name: "alternative options form",
			in: `{
		   		      "kind": "role",
		   		      "version": "v3",
		   		      "metadata": {"name": "name1"},
		   		      "spec": {
		                    "options": {
		                      "cert_format": "standard",
		                      "max_session_ttl": "20h",
		                      "port_forwarding": "yes",
		                      "forward_agent": "yes",
		                      "client_idle_timeout": "never",
		                      "disconnect_expired_cert": "no",
		                      "enhanced_recording": ["command", "network"]
		                    },
		                    "allow": {
		                      "node_labels": {"a": "b"},
		                      "app_labels": {"a": "b"},
		                      "kubernetes_labels": {"c": "d"},
		                      "db_labels": {"e": "f"},
		                      "namespaces": ["default"],
		                      "rules": [
		                        {
		                          "resources": ["role"],
		                          "verbs": ["read", "list"],
		                          "where": "contains(user.spec.traits[\"groups\"], \"prod\")",
		                          "actions": [
		                             "log(\"info\", \"log entry\")"
		                          ]
		                        }
		                      ]
		                    },
		                    "deny": {
		                      "logins": ["c"]
		                    }
		   		      }
		   		    }`,
			role: RoleV3{
				Kind:    KindRole,
				Version: V3,
				Metadata: Metadata{
					Name:      "name1",
					Namespace: defaults.Namespace,
				},
				Spec: RoleSpecV3{
					Options: RoleOptions{
						CertificateFormat:     teleport.CertificateFormatStandard,
						ForwardAgent:          NewBool(true),
						MaxSessionTTL:         NewDuration(20 * time.Hour),
						PortForwarding:        NewBoolOption(true),
						ClientIdleTimeout:     NewDuration(0),
						DisconnectExpiredCert: NewBool(false),
						BPF:                   defaults.EnhancedEvents(),
					},
					Allow: RoleConditions{
						NodeLabels:       Labels{"a": []string{"b"}},
						AppLabels:        Labels{"a": []string{"b"}},
						KubernetesLabels: Labels{"c": []string{"d"}},
						DatabaseLabels:   Labels{"e": []string{"f"}},
						Namespaces:       []string{"default"},
						Rules: []Rule{
							{
								Resources: []string{KindRole},
								Verbs:     []string{VerbRead, VerbList},
								Where:     "contains(user.spec.traits[\"groups\"], \"prod\")",
								Actions: []string{
									"log(\"info\", \"log entry\")",
								},
							},
						},
					},
					Deny: RoleConditions{
						Namespaces: []string{defaults.Namespace},
						Logins:     []string{"c"},
					},
				},
			},
			error: nil,
		},
		{
			name: "non-scalar and scalar values of labels",
			in: `{
		   		      "kind": "role",
		   		      "version": "v3",
		   		      "metadata": {"name": "name1"},
		   		      "spec": {
		                    "options": {
		                      "cert_format": "standard",
		                      "max_session_ttl": "20h",
		                      "port_forwarding": "yes",
		                      "forward_agent": "yes",
		                      "client_idle_timeout": "never",
		                      "disconnect_expired_cert": "no",
		                      "enhanced_recording": ["command", "network"]
		                    },
		                    "allow": {
		                      "node_labels": {"a": "b", "key": ["val"], "key2": ["val2", "val3"]},
		                      "app_labels": {"a": "b", "key": ["val"], "key2": ["val2", "val3"]},
		                      "kubernetes_labels": {"a": "b", "key": ["val"], "key2": ["val2", "val3"]},
		                      "db_labels": {"a": "b", "key": ["val"], "key2": ["val2", "val3"]}
		                    },
		                    "deny": {
		                      "logins": ["c"]
		                    }
		   		      }
		   		    }`,
			role: RoleV3{
				Kind:    KindRole,
				Version: V3,
				Metadata: Metadata{
					Name:      "name1",
					Namespace: defaults.Namespace,
				},
				Spec: RoleSpecV3{
					Options: RoleOptions{
						CertificateFormat:     teleport.CertificateFormatStandard,
						ForwardAgent:          NewBool(true),
						MaxSessionTTL:         NewDuration(20 * time.Hour),
						PortForwarding:        NewBoolOption(true),
						ClientIdleTimeout:     NewDuration(0),
						DisconnectExpiredCert: NewBool(false),
						BPF:                   defaults.EnhancedEvents(),
					},
					Allow: RoleConditions{
						NodeLabels: Labels{
							"a":    []string{"b"},
							"key":  []string{"val"},
							"key2": []string{"val2", "val3"},
						},
						AppLabels: Labels{
							"a":    []string{"b"},
							"key":  []string{"val"},
							"key2": []string{"val2", "val3"},
						},
						KubernetesLabels: Labels{
							"a":    []string{"b"},
							"key":  []string{"val"},
							"key2": []string{"val2", "val3"},
						},
						DatabaseLabels: Labels{
							"a":    []string{"b"},
							"key":  []string{"val"},
							"key2": []string{"val2", "val3"},
						},
						Namespaces: []string{"default"},
					},
					Deny: RoleConditions{
						Namespaces: []string{defaults.Namespace},
						Logins:     []string{"c"},
					},
				},
			},
			error: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			role, err := UnmarshalRole([]byte(tc.in))
			if tc.error != nil {
				require.Error(t, err)
				if tc.matchMessage != "" {
					require.Contains(t, err.Error(), tc.matchMessage)
				}
			} else {
				require.NoError(t, err)
				require.True(t, role.Equals(&tc.role))

				err := ValidateRole(role)
				require.NoError(t, err)

				out, err := json.Marshal(role)
				require.NoError(t, err)

				role2, err := UnmarshalRole(out)
				require.NoError(t, err)
				require.True(t, role2.Equals(&tc.role))
			}
		})
	}
}

func TestValidateRole(t *testing.T) {
	var tests = []struct {
		name         string
		spec         RoleSpecV3
		err          error
		matchMessage string
	}{
		{
			name: "valid syntax",
			spec: RoleSpecV3{
				Allow: RoleConditions{
					Logins: []string{`{{external["http://schemas.microsoft.com/ws/2008/06/identity/claims/windowsaccountname"]}}`},
				},
			},
		},
		{
			name: "invalid role condition login syntax",
			spec: RoleSpecV3{
				Allow: RoleConditions{
					Logins: []string{"{{foo"},
				},
			},
			err:          trace.BadParameter(""),
			matchMessage: "invalid login found",
		},
		{
			name: "unsupported function in actions",
			spec: RoleSpecV3{
				Allow: RoleConditions{
					Logins: []string{`{{external["http://schemas.microsoft.com/ws/2008/06/identity/claims/windowsaccountname"]}}`},
					Rules: []Rule{
						{
							Resources: []string{"role"},
							Verbs:     []string{"read", "list"},
							Where:     "containz(user.spec.traits[\"groups\"], \"prod\")",
						},
					},
				},
			},
			err:          trace.BadParameter(""),
			matchMessage: "unsupported function: containz",
		},
		{
			name: "unsupported function in where",
			spec: RoleSpecV3{
				Allow: RoleConditions{
					Logins: []string{`{{external["http://schemas.microsoft.com/ws/2008/06/identity/claims/windowsaccountname"]}}`},
					Rules: []Rule{
						{
							Resources: []string{"role"},
							Verbs:     []string{"read", "list"},
							Where:     "contains(user.spec.traits[\"groups\"], \"prod\")",
							Actions:   []string{"zzz(\"info\", \"log entry\")"},
						},
					},
				},
			},
			err:          trace.BadParameter(""),
			matchMessage: "unsupported function: zzz",
		},
	}

	for _, tc := range tests {
		err := ValidateRole(&types.RoleV3{
			Metadata: Metadata{
				Name:      "name1",
				Namespace: defaults.Namespace,
			},
			Spec: tc.spec,
		})
		if tc.err != nil {
			require.Error(t, err, tc.name)
			if tc.matchMessage != "" {
				require.Contains(t, err.Error(), tc.matchMessage)
			}
		} else {
			require.NoError(t, err, tc.name)
		}
	}
}

// TestLabelCompatibility makes sure that labels
// are serialized in format understood by older servers with
// scalar labels
func TestLabelCompatibility(t *testing.T) {
	labels := Labels{
		"key": []string{"val"},
	}
	data, err := json.Marshal(labels)
	require.NoError(t, err)

	var out map[string]string
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"key": "val"}, out)
}

func TestCheckAccessToServer(t *testing.T) {
	type check struct {
		server    Server
		hasAccess bool
		login     string
	}
	serverA := &ServerV2{
		Metadata: Metadata{
			Name: "a",
		},
	}
	serverB := &ServerV2{
		Metadata: Metadata{
			Name:      "b",
			Namespace: defaults.Namespace,
			Labels:    map[string]string{"role": "worker", "status": "follower"},
		},
	}
	namespaceC := "namespace-c"
	serverC := &ServerV2{
		Metadata: Metadata{
			Name:      "c",
			Namespace: namespaceC,
			Labels:    map[string]string{"role": "db", "status": "follower"},
		},
	}
	serverC2 := &ServerV2{
		Metadata: Metadata{
			Name:      "c2",
			Namespace: namespaceC,
			Labels:    map[string]string{"role": "db01", "status": "follower01"},
		},
	}
	testCases := []struct {
		name   string
		roles  []RoleV3
		checks []check
	}{
		{
			name:  "empty role set has access to nothing",
			roles: []RoleV3{},
			checks: []check{
				{server: serverA, login: "root", hasAccess: false},
				{server: serverB, login: "root", hasAccess: false},
				{server: serverC, login: "root", hasAccess: false},
			},
		},
		{
			name: "role is limited to default namespace",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Namespaces: []string{defaults.Namespace},
							Logins:     []string{"admin"},
							NodeLabels: Labels{Wildcard: []string{Wildcard}},
						},
					},
				},
			},
			checks: []check{
				{server: serverA, login: "root", hasAccess: false},
				{server: serverA, login: "admin", hasAccess: true},
				{server: serverB, login: "root", hasAccess: false},
				{server: serverB, login: "admin", hasAccess: true},
				{server: serverC, login: "root", hasAccess: false},
				{server: serverC, login: "admin", hasAccess: false},
			},
		},
		{
			name: "role is limited to labels in default namespace",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:     []string{"admin"},
							NodeLabels: Labels{"role": []string{"worker"}},
							Namespaces: []string{defaults.Namespace},
						},
					},
				},
			},
			checks: []check{
				{server: serverA, login: "root", hasAccess: false},
				{server: serverA, login: "admin", hasAccess: false},
				{server: serverB, login: "root", hasAccess: false},
				{server: serverB, login: "admin", hasAccess: true},
				{server: serverC, login: "root", hasAccess: false},
				{server: serverC, login: "admin", hasAccess: false},
			},
		},
		{
			name: "role matches any label out of multiple labels",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:     []string{"admin"},
							NodeLabels: Labels{"role": []string{"worker2", "worker"}},
							Namespaces: []string{defaults.Namespace},
						},
					},
				},
			},
			checks: []check{
				{server: serverA, login: "root", hasAccess: false},
				{server: serverA, login: "admin", hasAccess: false},
				{server: serverB, login: "root", hasAccess: false},
				{server: serverB, login: "admin", hasAccess: true},
				{server: serverC, login: "root", hasAccess: false},
				{server: serverC, login: "admin", hasAccess: false},
			},
		},
		{
			name: "node_labels with empty list value matches nothing",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:     []string{"admin"},
							NodeLabels: Labels{"role": []string{}},
							Namespaces: []string{defaults.Namespace},
						},
					},
				},
			},
			checks: []check{
				{server: serverA, login: "root", hasAccess: false},
				{server: serverA, login: "admin", hasAccess: false},
				{server: serverB, login: "root", hasAccess: false},
				{server: serverB, login: "admin", hasAccess: false},
				{server: serverC, login: "root", hasAccess: false},
				{server: serverC, login: "admin", hasAccess: false},
			},
		},
		{
			name: "one role is more permissive than another",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:     []string{"admin"},
							NodeLabels: Labels{"role": []string{"worker"}},
							Namespaces: []string{defaults.Namespace},
						},
					},
				},
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:     []string{"root", "admin"},
							NodeLabels: Labels{Wildcard: []string{Wildcard}},
							Namespaces: []string{Wildcard},
						},
					},
				},
			},
			checks: []check{
				{server: serverA, login: "root", hasAccess: true},
				{server: serverA, login: "admin", hasAccess: true},
				{server: serverB, login: "root", hasAccess: true},
				{server: serverB, login: "admin", hasAccess: true},
				{server: serverC, login: "root", hasAccess: true},
				{server: serverC, login: "admin", hasAccess: true},
			},
		},
		{
			name: "one role needs to access servers sharing the partially same label value",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: namespaceC,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:     []string{"admin"},
							NodeLabels: Labels{"role": []string{"^db(.*)$"}, "status": []string{"follow*"}},
							Namespaces: []string{namespaceC},
						},
					},
				},
			},
			checks: []check{
				{server: serverA, login: "root", hasAccess: false},
				{server: serverA, login: "admin", hasAccess: false},
				{server: serverB, login: "root", hasAccess: false},
				{server: serverB, login: "admin", hasAccess: false},
				{server: serverC, login: "root", hasAccess: false},
				{server: serverC, login: "admin", hasAccess: true},
				{server: serverC2, login: "root", hasAccess: false},
				{server: serverC2, login: "admin", hasAccess: true},
			},
		},
		{
			name: "no logins means no access",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "somerole",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:     nil,
							NodeLabels: Labels{Wildcard: []string{Wildcard}},
							Namespaces: []string{Wildcard},
						},
					},
				},
			},
			checks: []check{
				{server: serverA, login: "root", hasAccess: false},
				{server: serverA, login: "admin", hasAccess: false},
				{server: serverB, login: "root", hasAccess: false},
				{server: serverB, login: "admin", hasAccess: false},
				{server: serverC, login: "root", hasAccess: false},
				{server: serverC, login: "admin", hasAccess: false},
			},
		},
	}
	for i, tc := range testCases {

		var set RoleSet
		for i := range tc.roles {
			set = append(set, &tc.roles[i])
		}
		for j, check := range tc.checks {
			comment := fmt.Sprintf("test case %v '%v', check %v", i, tc.name, j)
			result := set.CheckAccessToServer(check.login, check.server)
			if check.hasAccess {
				require.NoError(t, result, comment)
			} else {
				require.True(t, trace.IsAccessDenied(result), comment)
			}
		}
	}
}

func TestCheckAccessToRemoteCluster(t *testing.T) {
	type check struct {
		rc        RemoteCluster
		hasAccess bool
	}
	rcA := &RemoteClusterV3{
		Metadata: Metadata{
			Name: "a",
		},
	}
	rcB := &RemoteClusterV3{
		Metadata: Metadata{
			Name:   "b",
			Labels: map[string]string{"role": "worker", "status": "follower"},
		},
	}
	rcC := &RemoteClusterV3{
		Metadata: Metadata{
			Name:   "c",
			Labels: map[string]string{"role": "db", "status": "follower"},
		},
	}
	testCases := []struct {
		name   string
		roles  []RoleV3
		checks []check
	}{
		{
			name:  "empty role set has access to nothing",
			roles: []RoleV3{},
			checks: []check{
				{rc: rcA, hasAccess: false},
				{rc: rcB, hasAccess: false},
				{rc: rcC, hasAccess: false},
			},
		},
		{
			name: "role matches any label out of multiple labels",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:        []string{"admin"},
							ClusterLabels: Labels{"role": []string{"worker2", "worker"}},
							Namespaces:    []string{defaults.Namespace},
						},
					},
				},
			},
			checks: []check{
				{rc: rcA, hasAccess: false},
				{rc: rcB, hasAccess: true},
				{rc: rcC, hasAccess: false},
			},
		},
		{
			name: "wildcard matches anything",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:        []string{"admin"},
							ClusterLabels: Labels{Wildcard: []string{Wildcard}},
							Namespaces:    []string{defaults.Namespace},
						},
					},
				},
			},
			checks: []check{
				{rc: rcA, hasAccess: true},
				{rc: rcB, hasAccess: true},
				{rc: rcC, hasAccess: true},
			},
		},
		{
			name: "role with no labels will match clusters with no labels, but no others",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Namespaces: []string{defaults.Namespace},
						},
					},
				},
			},
			checks: []check{
				{rc: rcA, hasAccess: true},
				{rc: rcB, hasAccess: false},
				{rc: rcC, hasAccess: false},
			},
		},
		{
			name: "any role in the set with labels in the set makes the set to match labels",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							ClusterLabels: Labels{"role": []string{"worker"}},
							Namespaces:    []string{defaults.Namespace},
						},
					},
				},
				{
					Metadata: Metadata{
						Name:      "name2",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Namespaces: []string{defaults.Namespace},
						},
					},
				},
			},
			checks: []check{
				{rc: rcA, hasAccess: false},
				{rc: rcB, hasAccess: true},
				{rc: rcC, hasAccess: false},
			},
		},
		{
			name: "cluster_labels with empty list value matches nothing",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:        []string{"admin"},
							ClusterLabels: Labels{"role": []string{}},
							Namespaces:    []string{defaults.Namespace},
						},
					},
				},
			},
			checks: []check{
				{rc: rcA, hasAccess: false},
				{rc: rcB, hasAccess: false},
				{rc: rcC, hasAccess: false},
			},
		},
		{
			name: "one role is more permissive than another",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:        []string{"admin"},
							ClusterLabels: Labels{"role": []string{"worker"}},
							Namespaces:    []string{defaults.Namespace},
						},
					},
				},
				{
					Metadata: Metadata{
						Name:      "name2",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:        []string{"root", "admin"},
							ClusterLabels: Labels{Wildcard: []string{Wildcard}},
							Namespaces:    []string{Wildcard},
						},
					},
				},
			},
			checks: []check{
				{rc: rcA, hasAccess: true},
				{rc: rcB, hasAccess: true},
				{rc: rcC, hasAccess: true},
			},
		},
		{
			name: "regexp label match",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Options: RoleOptions{
							MaxSessionTTL: Duration(20 * time.Hour),
						},
						Allow: RoleConditions{
							Logins:        []string{"admin"},
							ClusterLabels: Labels{"role": []string{"^db(.*)$"}, "status": []string{"follow*"}},
							Namespaces:    []string{defaults.Namespace},
						},
					},
				},
			},
			checks: []check{
				{rc: rcA, hasAccess: false},
				{rc: rcB, hasAccess: false},
				{rc: rcC, hasAccess: true},
			},
		},
	}
	for i, tc := range testCases {
		var set RoleSet
		for i := range tc.roles {
			set = append(set, &tc.roles[i])
		}
		for j, check := range tc.checks {
			comment := fmt.Sprintf("test case %v '%v', check %v", i, tc.name, j)
			result := set.CheckAccessToRemoteCluster(check.rc)
			if check.hasAccess {
				require.NoError(t, result, comment)
			} else {
				require.True(t, trace.IsAccessDenied(result), fmt.Sprintf("%v: %v", comment, result))
			}
		}
	}
}

// testContext overrides context and captures log writes in action
type testContext struct {
	Context
	// Buffer captures log writes
	buffer *bytes.Buffer
}

// Write is implemented explicitly to avoid collision
// of String methods when embedding
func (t *testContext) Write(data []byte) (int, error) {
	return t.buffer.Write(data)
}

func TestCheckRuleAccess(t *testing.T) {
	type check struct {
		hasAccess   bool
		verb        string
		namespace   string
		rule        string
		context     testContext
		matchBuffer string
	}
	testCases := []struct {
		name   string
		roles  []RoleV3
		checks []check
	}{
		{
			name:  "0 - empty role set has access to nothing",
			roles: []RoleV3{},
			checks: []check{
				{rule: KindUser, verb: ActionWrite, namespace: defaults.Namespace, hasAccess: false},
			},
		},
		{
			name: "1 - user can read session but can't list in default namespace",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Allow: RoleConditions{
							Namespaces: []string{defaults.Namespace},
							Rules: []Rule{
								NewRule(KindSSHSession, []string{VerbRead}),
							},
						},
					},
				},
			},
			checks: []check{
				{rule: KindSSHSession, verb: VerbRead, namespace: defaults.Namespace, hasAccess: true},
				{rule: KindSSHSession, verb: VerbList, namespace: defaults.Namespace, hasAccess: false},
			},
		},
		{
			name: "2 - user can read sessions in system namespace and create stuff in default namespace",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Allow: RoleConditions{
							Namespaces: []string{"system"},
							Rules: []Rule{
								NewRule(KindSSHSession, []string{VerbRead}),
							},
						},
					},
				},
				{
					Metadata: Metadata{
						Name:      "name2",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Allow: RoleConditions{
							Namespaces: []string{defaults.Namespace},
							Rules: []Rule{
								NewRule(KindSSHSession, []string{VerbCreate, VerbRead}),
							},
						},
					},
				},
			},
			checks: []check{
				{rule: KindSSHSession, verb: VerbRead, namespace: defaults.Namespace, hasAccess: true},
				{rule: KindSSHSession, verb: VerbCreate, namespace: defaults.Namespace, hasAccess: true},
				{rule: KindSSHSession, verb: VerbCreate, namespace: "system", hasAccess: false},
				{rule: KindRole, verb: VerbRead, namespace: defaults.Namespace, hasAccess: false},
			},
		},
		{
			name: "3 - deny rules override allow rules",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Deny: RoleConditions{
							Namespaces: []string{defaults.Namespace},
							Rules: []Rule{
								NewRule(KindSSHSession, []string{VerbCreate}),
							},
						},
						Allow: RoleConditions{
							Namespaces: []string{defaults.Namespace},
							Rules: []Rule{
								NewRule(KindSSHSession, []string{VerbCreate}),
							},
						},
					},
				},
			},
			checks: []check{
				{rule: KindSSHSession, verb: VerbCreate, namespace: defaults.Namespace, hasAccess: false},
			},
		},
		{
			name: "4 - user can read sessions if trait matches",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Allow: RoleConditions{
							Namespaces: []string{defaults.Namespace},
							Rules: []Rule{
								{
									Resources: []string{KindSession},
									Verbs:     []string{VerbRead},
									Where:     `contains(user.spec.traits["group"], "prod")`,
									Actions: []string{
										`log("info", "4 - tc match for user %v", user.metadata.name)`,
									},
								},
							},
						},
					},
				},
			},
			checks: []check{
				{rule: KindSession, verb: VerbRead, namespace: defaults.Namespace, hasAccess: false},
				{rule: KindSession, verb: VerbList, namespace: defaults.Namespace, hasAccess: false},
				{
					context: testContext{
						buffer: &bytes.Buffer{},
						Context: Context{
							User: &UserV2{
								Metadata: Metadata{
									Name: "bob",
								},
								Spec: UserSpecV2{
									Traits: map[string][]string{
										"group": {"dev", "prod"},
									},
								},
							},
						},
					},
					rule:      KindSession,
					verb:      VerbRead,
					namespace: defaults.Namespace,
					hasAccess: true,
				},
				{
					context: testContext{
						buffer: &bytes.Buffer{},
						Context: Context{
							User: &UserV2{
								Spec: UserSpecV2{
									Traits: map[string][]string{
										"group": {"dev"},
									},
								},
							},
						},
					},
					rule:      KindSession,
					verb:      VerbRead,
					namespace: defaults.Namespace,
					hasAccess: false,
				},
			},
		},
		{
			name: "5 - user can read role if role has label",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Allow: RoleConditions{
							Namespaces: []string{defaults.Namespace},
							Rules: []Rule{
								{
									Resources: []string{KindRole},
									Verbs:     []string{VerbRead},
									Where:     `equals(resource.metadata.labels["team"], "dev")`,
									Actions: []string{
										`log("error", "4 - tc match")`,
									},
								},
							},
						},
					},
				},
			},
			checks: []check{
				{rule: KindRole, verb: VerbRead, namespace: defaults.Namespace, hasAccess: false},
				{rule: KindRole, verb: VerbList, namespace: defaults.Namespace, hasAccess: false},
				{
					context: testContext{
						buffer: &bytes.Buffer{},
						Context: Context{
							Resource: &RoleV3{
								Metadata: Metadata{
									Labels: map[string]string{"team": "dev"},
								},
							},
						},
					},
					rule:      KindRole,
					verb:      VerbRead,
					namespace: defaults.Namespace,
					hasAccess: true,
				},
			},
		},
		{
			name: "More specific rule wins",
			roles: []RoleV3{
				{
					Metadata: Metadata{
						Name:      "name1",
						Namespace: defaults.Namespace,
					},
					Spec: RoleSpecV3{
						Allow: RoleConditions{
							Namespaces: []string{defaults.Namespace},
							Rules: []Rule{
								{
									Resources: []string{Wildcard},
									Verbs:     []string{Wildcard},
								},
								{
									Resources: []string{KindRole},
									Verbs:     []string{VerbRead},
									Where:     `equals(resource.metadata.labels["team"], "dev")`,
									Actions: []string{
										`log("info", "matched more specific rule")`,
									},
								},
							},
						},
					},
				},
			},
			checks: []check{
				{
					context: testContext{
						buffer: &bytes.Buffer{},
						Context: Context{
							Resource: &RoleV3{
								Metadata: Metadata{
									Labels: map[string]string{"team": "dev"},
								},
							},
						},
					},
					rule:        KindRole,
					verb:        VerbRead,
					namespace:   defaults.Namespace,
					hasAccess:   true,
					matchBuffer: "more specific rule",
				},
			},
		},
	}
	for i, tc := range testCases {
		var set RoleSet
		for i := range tc.roles {
			set = append(set, &tc.roles[i])
		}
		for j, check := range tc.checks {
			comment := fmt.Sprintf("test case %v '%v', check %v", i, tc.name, j)
			result := set.CheckAccessToRule(&check.context, check.namespace, check.rule, check.verb, false)
			if check.hasAccess {
				require.NoError(t, result, comment)
			} else {
				require.True(t, trace.IsAccessDenied(result), comment)
			}
			if check.matchBuffer != "" {
				require.Contains(t, check.context.buffer.String(), check.matchBuffer, comment)
			}
		}
	}
}

func TestCheckRuleSorting(t *testing.T) {
	testCases := []struct {
		name  string
		rules []Rule
		set   RuleSet
	}{
		{
			name: "single rule set sorts OK",
			rules: []Rule{
				{
					Resources: []string{KindUser},
					Verbs:     []string{VerbCreate},
				},
			},
			set: RuleSet{
				KindUser: []Rule{
					{
						Resources: []string{KindUser},
						Verbs:     []string{VerbCreate},
					},
				},
			},
		},
		{
			name: "rule with where section is more specific",
			rules: []Rule{
				{
					Resources: []string{KindUser},
					Verbs:     []string{VerbCreate},
				},
				{
					Resources: []string{KindUser},
					Verbs:     []string{VerbCreate},
					Where:     "contains(user.spec.traits[\"groups\"], \"prod\")",
				},
			},
			set: RuleSet{
				KindUser: []Rule{
					{
						Resources: []string{KindUser},
						Verbs:     []string{VerbCreate},
						Where:     "contains(user.spec.traits[\"groups\"], \"prod\")",
					},
					{
						Resources: []string{KindUser},
						Verbs:     []string{VerbCreate},
					},
				},
			},
		},
		{
			name: "rule with action is more specific",
			rules: []Rule{
				{
					Resources: []string{KindUser},
					Verbs:     []string{VerbCreate},

					Where: "contains(user.spec.traits[\"groups\"], \"prod\")",
				},
				{
					Resources: []string{KindUser},
					Verbs:     []string{VerbCreate},
					Where:     "contains(user.spec.traits[\"groups\"], \"prod\")",
					Actions: []string{
						"log(\"info\", \"log entry\")",
					},
				},
			},
			set: RuleSet{
				KindUser: []Rule{
					{
						Resources: []string{KindUser},
						Verbs:     []string{VerbCreate},
						Where:     "contains(user.spec.traits[\"groups\"], \"prod\")",
						Actions: []string{
							"log(\"info\", \"log entry\")",
						},
					},
					{
						Resources: []string{KindUser},
						Verbs:     []string{VerbCreate},
						Where:     "contains(user.spec.traits[\"groups\"], \"prod\")",
					},
				},
			},
		},
	}
	for i, tc := range testCases {
		comment := fmt.Sprintf("test case %v '%v'", i, tc.name)
		out := MakeRuleSet(tc.rules)
		require.Equal(t, tc.set, out, comment)
	}
}

func TestApplyTraits(t *testing.T) {
	type rule struct {
		inLogins      []string
		outLogins     []string
		inLabels      Labels
		outLabels     Labels
		inKubeGroups  []string
		outKubeGroups []string
		inKubeUsers   []string
		outKubeUsers  []string
		inDBNames     []string
		outDBNames    []string
		inDBUsers     []string
		outDBUsers    []string
	}
	var tests = []struct {
		comment  string
		inTraits map[string][]string
		allow    rule
		deny     rule
	}{

		{
			comment: "logins substitute in allow rule",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inLogins:  []string{`{{external.foo}}`, "root"},
				outLogins: []string{"bar", "root"},
			},
		},
		{
			comment: "logins substitute in allow rule with function",
			inTraits: map[string][]string{
				"foo": {"Bar <bar@example.com>"},
			},
			allow: rule{
				inLogins:  []string{`{{email.local(external.foo)}}`, "root"},
				outLogins: []string{"bar", "root"},
			},
		},
		{
			comment: "logins substitute in deny rule",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			deny: rule{
				inLogins:  []string{`{{external.foo}}`},
				outLogins: []string{"bar"},
			},
		},
		{
			comment: "kube group substitute in allow rule",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inKubeGroups:  []string{`{{external.foo}}`, "root"},
				outKubeGroups: []string{"bar", "root"},
			},
		},
		{
			comment: "kube group substitute in deny rule",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			deny: rule{
				inKubeGroups:  []string{`{{external.foo}}`, "root"},
				outKubeGroups: []string{"bar", "root"},
			},
		},
		{
			comment: "kube user interpolation in allow rule",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inKubeUsers:  []string{`IAM#{{external.foo}};`},
				outKubeUsers: []string{"IAM#bar;"},
			},
		},
		{
			comment: "kube users interpolation in deny rule",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			deny: rule{
				inKubeUsers:  []string{`IAM#{{external.foo}};`},
				outKubeUsers: []string{"IAM#bar;"},
			},
		},
		{
			comment: "database name/user external vars in allow rule",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inDBNames:  []string{"{{external.foo}}", "{{external.baz}}", "postgres"},
				outDBNames: []string{"bar", "postgres"},
				inDBUsers:  []string{"{{external.foo}}", "{{external.baz}}", "postgres"},
				outDBUsers: []string{"bar", "postgres"},
			},
		},
		{
			comment: "database name/user external vars in deny rule",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			deny: rule{
				inDBNames:  []string{"{{external.foo}}", "{{external.baz}}", "postgres"},
				outDBNames: []string{"bar", "postgres"},
				inDBUsers:  []string{"{{external.foo}}", "{{external.baz}}", "postgres"},
				outDBUsers: []string{"bar", "postgres"},
			},
		},
		{
			comment: "database name/user internal vars in allow rule",
			inTraits: map[string][]string{
				"db_names": {"db1", "db2"},
				"db_users": {"alice"},
			},
			allow: rule{
				inDBNames:  []string{"{{internal.db_names}}", "{{internal.foo}}", "postgres"},
				outDBNames: []string{"db1", "db2", "postgres"},
				inDBUsers:  []string{"{{internal.db_users}}", "{{internal.foo}}", "postgres"},
				outDBUsers: []string{"alice", "postgres"},
			},
		},
		{
			comment: "database name/user internal vars in deny rule",
			inTraits: map[string][]string{
				"db_names": {"db1", "db2"},
				"db_users": {"alice"},
			},
			deny: rule{
				inDBNames:  []string{"{{internal.db_names}}", "{{internal.foo}}", "postgres"},
				outDBNames: []string{"db1", "db2", "postgres"},
				inDBUsers:  []string{"{{internal.db_users}}", "{{internal.foo}}", "postgres"},
				outDBUsers: []string{"alice", "postgres"},
			},
		},
		{
			comment: "no variable in logins",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inLogins:  []string{"root"},
				outLogins: []string{"root"},
			},
		},
		{
			comment: "invalid variable in logins does not get passed along",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inLogins: []string{`external.foo}}`},
			},
		},
		{
			comment: "invalid function call in logins does not get passed along",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inLogins: []string{`{{email.local(external.foo, 1)}}`},
			},
		},
		{
			comment: "invalid function call in logins does not get passed along",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inLogins: []string{`{{email.local()}}`},
			},
		},
		{
			comment: "invalid function call in logins does not get passed along",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inLogins: []string{`{{email.local(email.local)}}`, `{{email.local(email.local())}}`},
			},
		},
		{
			comment: "variable in logins, none in traits",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inLogins:  []string{`{{internal.bar}}`, "root"},
				outLogins: []string{"root"},
			},
		},
		{
			comment: "multiple variables in traits",
			inTraits: map[string][]string{
				"logins": {"bar", "baz"},
			},
			allow: rule{
				inLogins:  []string{`{{internal.logins}}`, "root"},
				outLogins: []string{"bar", "baz", "root"},
			},
		},
		{
			comment: "deduplicate",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inLogins:  []string{`{{external.foo}}`, "bar"},
				outLogins: []string{"bar"},
			},
		},
		{
			comment: "invalid unix login",
			inTraits: map[string][]string{
				"foo": {"-foo"},
			},
			allow: rule{
				inLogins:  []string{`{{external.foo}}`, "bar"},
				outLogins: []string{"bar"},
			},
		},
		{
			comment: "label substitute in allow and deny rule",
			inTraits: map[string][]string{
				"foo":   {"bar"},
				"hello": {"there"},
			},
			allow: rule{
				inLabels:  Labels{`{{external.foo}}`: []string{"{{external.hello}}"}},
				outLabels: Labels{`bar`: []string{"there"}},
			},
			deny: rule{
				inLabels:  Labels{`{{external.hello}}`: []string{"{{external.foo}}"}},
				outLabels: Labels{`there`: []string{"bar"}},
			},
		},

		{
			comment: "missing node variables are set to empty during substitution",
			inTraits: map[string][]string{
				"foo": {"bar"},
			},
			allow: rule{
				inLabels: Labels{
					`{{external.foo}}`:     []string{"value"},
					`{{external.missing}}`: []string{"missing"},
					`missing`:              []string{"{{external.missing}}", "othervalue"},
				},
				outLabels: Labels{
					`bar`:     []string{"value"},
					"missing": []string{"", "othervalue"},
					"":        []string{"missing"},
				},
			},
		},

		{
			comment: "the first variable value is picked for label keys",
			inTraits: map[string][]string{
				"foo": {"bar", "baz"},
			},
			allow: rule{
				inLabels:  Labels{`{{external.foo}}`: []string{"value"}},
				outLabels: Labels{`bar`: []string{"value"}},
			},
		},

		{
			comment: "all values are expanded for label values",
			inTraits: map[string][]string{
				"foo": {"bar", "baz"},
			},
			allow: rule{
				inLabels:  Labels{`key`: []string{`{{external.foo}}`}},
				outLabels: Labels{`key`: []string{"bar", "baz"}},
			},
		},
	}

	for i, tt := range tests {
		comment := fmt.Sprintf("Test %v %v", i, tt.comment)

		role := &RoleV3{
			Kind:    KindRole,
			Version: V3,
			Metadata: Metadata{
				Name:      "name1",
				Namespace: defaults.Namespace,
			},
			Spec: RoleSpecV3{
				Allow: RoleConditions{
					Logins:        tt.allow.inLogins,
					NodeLabels:    tt.allow.inLabels,
					ClusterLabels: tt.allow.inLabels,
					KubeGroups:    tt.allow.inKubeGroups,
					KubeUsers:     tt.allow.inKubeUsers,
					DatabaseNames: tt.allow.inDBNames,
					DatabaseUsers: tt.allow.inDBUsers,
				},
				Deny: RoleConditions{
					Logins:        tt.deny.inLogins,
					NodeLabels:    tt.deny.inLabels,
					ClusterLabels: tt.deny.inLabels,
					KubeGroups:    tt.deny.inKubeGroups,
					KubeUsers:     tt.deny.inKubeUsers,
					DatabaseNames: tt.deny.inDBNames,
					DatabaseUsers: tt.deny.inDBUsers,
				},
			},
		}

		outRole := ApplyTraits(role, tt.inTraits)
		require.Equal(t, outRole.GetLogins(Allow), tt.allow.outLogins, comment)
		require.Equal(t, outRole.GetNodeLabels(Allow), tt.allow.outLabels, comment)
		require.Equal(t, outRole.GetClusterLabels(Allow), tt.allow.outLabels, comment)
		require.Equal(t, outRole.GetKubeGroups(Allow), tt.allow.outKubeGroups, comment)
		require.Equal(t, outRole.GetKubeUsers(Allow), tt.allow.outKubeUsers, comment)
		require.Equal(t, outRole.GetDatabaseNames(Allow), tt.allow.outDBNames, comment)
		require.Equal(t, outRole.GetDatabaseUsers(Allow), tt.allow.outDBUsers, comment)

		require.Equal(t, outRole.GetLogins(Deny), tt.deny.outLogins, comment)
		require.Equal(t, outRole.GetNodeLabels(Deny), tt.deny.outLabels, comment)
		require.Equal(t, outRole.GetClusterLabels(Deny), tt.deny.outLabels, comment)
		require.Equal(t, outRole.GetKubeGroups(Deny), tt.deny.outKubeGroups, comment)
		require.Equal(t, outRole.GetKubeUsers(Deny), tt.deny.outKubeUsers, comment)
		require.Equal(t, outRole.GetDatabaseNames(Deny), tt.deny.outDBNames, comment)
		require.Equal(t, outRole.GetDatabaseUsers(Deny), tt.deny.outDBUsers, comment)
	}
}

// TestExtractFrom makes sure roles and traits are extracted from SSH and TLS
// certificates not services.User.
func TestExtractFrom(t *testing.T) {
	origRoles := []string{"admin"}
	origTraits := wrappers.Traits(map[string][]string{
		"login": {"foo"},
	})

	// Create a SSH certificate.
	pubkey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(fixtures.UserCertificateStandard))
	require.NoError(t, err)
	cert, ok := pubkey.(*ssh.Certificate)
	require.True(t, ok)

	// Create a TLS identity.
	identity := &tlsca.Identity{
		Username: "foo",
		Groups:   origRoles,
		Traits:   origTraits,
	}

	// At this point, services.User and the certificate/identity are still in
	// sync. The roles and traits returned should be the same as the original.
	roles, traits, err := ExtractFromCertificate(&userGetter{
		roles:  origRoles,
		traits: origTraits,
	}, cert)
	require.NoError(t, err)
	require.Equal(t, roles, origRoles)
	require.Equal(t, traits, origTraits)

	roles, traits, err = ExtractFromIdentity(&userGetter{
		roles:  origRoles,
		traits: origTraits,
	}, *identity)
	require.NoError(t, err)
	require.Equal(t, roles, origRoles)
	require.Equal(t, traits, origTraits)

	// The backend now returns new roles and traits, however because the roles
	// and traits are extracted from the certificate/identity, the original
	// roles and traits will be returned.
	roles, traits, err = ExtractFromCertificate(&userGetter{
		roles: []string{"intern"},
		traits: wrappers.Traits(map[string][]string{
			"login": {"bar"},
		}),
	}, cert)
	require.NoError(t, err)
	require.Equal(t, roles, origRoles)
	require.Equal(t, traits, origTraits)

	roles, traits, err = ExtractFromIdentity(&userGetter{
		roles:  origRoles,
		traits: origTraits,
	}, *identity)
	require.NoError(t, err)
	require.Equal(t, roles, origRoles)
	require.Equal(t, traits, origTraits)
}

// TestExtractFromLegacy verifies that roles and traits are fetched
// from services.User for SSH certificates is the legacy format and TLS
// certificates that don't contain traits.
func TestExtractFromLegacy(t *testing.T) {
	origRoles := []string{"admin"}
	origTraits := wrappers.Traits(map[string][]string{
		"login": {"foo"},
	})

	// Create a SSH certificate in the legacy format.
	pubkey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(fixtures.UserCertificateLegacy))
	require.NoError(t, err)
	cert, ok := pubkey.(*ssh.Certificate)
	require.True(t, ok)

	// Create a TLS identity with only roles.
	identity := &tlsca.Identity{
		Username: "foo",
		Groups:   origRoles,
	}

	// At this point, services.User and the certificate/identity are still in
	// sync. The roles and traits returned should be the same as the original.
	roles, traits, err := ExtractFromCertificate(&userGetter{
		roles:  origRoles,
		traits: origTraits,
	}, cert)
	require.NoError(t, err)
	require.Equal(t, roles, origRoles)
	require.Equal(t, traits, origTraits)
	roles, traits, err = ExtractFromIdentity(&userGetter{
		roles:  origRoles,
		traits: origTraits,
	}, *identity)
	require.NoError(t, err)
	require.Equal(t, roles, origRoles)
	require.Equal(t, traits, origTraits)

	// The backend now returns new roles and traits, because the SSH certificate
	// is in the old standard format and the TLS identity is missing traits.
	newRoles := []string{"intern"}
	newTraits := wrappers.Traits(map[string][]string{
		"login": {"bar"},
	})
	roles, traits, err = ExtractFromCertificate(&userGetter{
		roles:  newRoles,
		traits: newTraits,
	}, cert)
	require.NoError(t, err)
	require.Equal(t, roles, newRoles)
	require.Equal(t, traits, newTraits)
	roles, traits, err = ExtractFromIdentity(&userGetter{
		roles:  newRoles,
		traits: newTraits,
	}, *identity)
	require.NoError(t, err)
	require.Equal(t, roles, newRoles)
	require.Equal(t, traits, newTraits)
}

// TestBoolOptions makes sure that bool options (like agent forwarding and
// port forwarding) can be disabled in a role.
func TestBoolOptions(t *testing.T) {
	var tests = []struct {
		inOptions           RoleOptions
		outCanPortForward   bool
		outCanForwardAgents bool
	}{
		// Setting options explicitly off should remain off.
		{
			inOptions: RoleOptions{
				ForwardAgent:   NewBool(false),
				PortForwarding: NewBoolOption(false),
			},
			outCanPortForward:   false,
			outCanForwardAgents: false,
		},
		// Not setting options should set port forwarding to true (default enabled)
		// and agent forwarding false (default disabled).
		{
			inOptions:           RoleOptions{},
			outCanPortForward:   true,
			outCanForwardAgents: false,
		},
		// Explicitly enabling should enable them.
		{
			inOptions: RoleOptions{
				ForwardAgent:   NewBool(true),
				PortForwarding: NewBoolOption(true),
			},
			outCanPortForward:   true,
			outCanForwardAgents: true,
		},
	}
	for _, tt := range tests {
		set := NewRoleSet(&RoleV3{
			Kind:    KindRole,
			Version: V3,
			Metadata: Metadata{
				Name:      "role-name",
				Namespace: defaults.Namespace,
			},
			Spec: RoleSpecV3{
				Options: tt.inOptions,
			},
		})
		require.Equal(t, tt.outCanPortForward, set.CanPortForward())
		require.Equal(t, tt.outCanForwardAgents, set.CanForwardAgents())
	}
}

func TestCheckAccessToDatabase(t *testing.T) {
	utils.InitLoggerForTests(testing.Verbose())
	dbStage := types.NewDatabaseServerV3("stage",
		map[string]string{"env": "stage"},
		types.DatabaseServerSpecV3{})
	dbProd := types.NewDatabaseServerV3("prod",
		map[string]string{"env": "prod"},
		types.DatabaseServerSpecV3{})
	roleDevStage := &RoleV3{
		Metadata: Metadata{Name: "dev-stage", Namespace: defaults.Namespace},
		Spec: RoleSpecV3{
			Allow: RoleConditions{
				Namespaces:     []string{defaults.Namespace},
				DatabaseLabels: Labels{"env": []string{"stage"}},
				DatabaseNames:  []string{Wildcard},
				DatabaseUsers:  []string{Wildcard},
			},
			Deny: RoleConditions{
				Namespaces:     []string{defaults.Namespace},
				DatabaseLabels: Labels{"env": []string{"stage"}},
				DatabaseNames:  []string{"supersecret"},
			},
		},
	}
	roleDevProd := &RoleV3{
		Metadata: Metadata{Name: "dev-prod", Namespace: defaults.Namespace},
		Spec: RoleSpecV3{
			Allow: RoleConditions{
				Namespaces:     []string{defaults.Namespace},
				DatabaseLabels: Labels{"env": []string{"prod"}},
				DatabaseNames:  []string{"test"},
				DatabaseUsers:  []string{"dev"},
			},
		},
	}
	// Database labels are not set in allow/deny rules on purpose to test
	// that they're set during check and set defaults below.
	roleDeny := &types.RoleV3{
		Metadata: Metadata{Name: "deny", Namespace: defaults.Namespace},
		Spec: RoleSpecV3{
			Allow: RoleConditions{
				Namespaces:    []string{defaults.Namespace},
				DatabaseNames: []string{Wildcard},
				DatabaseUsers: []string{Wildcard},
			},
			Deny: RoleConditions{
				Namespaces:    []string{defaults.Namespace},
				DatabaseNames: []string{"postgres"},
				DatabaseUsers: []string{"postgres"},
			},
		},
	}
	require.NoError(t, roleDeny.CheckAndSetDefaults())
	type access struct {
		server types.DatabaseServer
		dbName string
		dbUser string
		access bool
	}
	testCases := []struct {
		name   string
		roles  []*RoleV3
		access []access
	}{
		{
			name:  "developer allowed any username/database in stage database except one database",
			roles: []*RoleV3{roleDevStage, roleDevProd},
			access: []access{
				{server: dbStage, dbName: "superdb", dbUser: "superuser", access: true},
				{server: dbStage, dbName: "test", dbUser: "dev", access: true},
				{server: dbStage, dbName: "supersecret", dbUser: "dev", access: false},
			},
		},
		{
			name:  "developer allowed only specific username/database in prod database",
			roles: []*RoleV3{roleDevStage, roleDevProd},
			access: []access{
				{server: dbProd, dbName: "superdb", dbUser: "superuser", access: false},
				{server: dbProd, dbName: "test", dbUser: "dev", access: true},
				{server: dbProd, dbName: "superdb", dbUser: "dev", access: false},
				{server: dbProd, dbName: "test", dbUser: "superuser", access: false},
			},
		},
		{
			name:  "deny role denies access to specific database",
			roles: []*RoleV3{roleDeny},
			access: []access{
				{server: dbProd, dbName: "test", dbUser: "test", access: true},
				{server: dbProd, dbName: "postgres", dbUser: "postgres", access: false},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var set RoleSet
			for _, r := range tc.roles {
				set = append(set, r)
			}
			for _, access := range tc.access {
				err := set.CheckAccessToDatabase(access.server, access.dbName, access.dbUser)
				if access.access {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
					require.True(t, trace.IsAccessDenied(err))
				}
			}
		})
	}
}

func TestCheckDatabaseNamesAndUsers(t *testing.T) {
	roleEmpty := &RoleV3{
		Metadata: Metadata{Name: "roleA", Namespace: defaults.Namespace},
		Spec: RoleSpecV3{
			Options: RoleOptions{MaxSessionTTL: Duration(time.Hour)},
			Allow: RoleConditions{
				Namespaces: []string{defaults.Namespace},
			},
		},
	}
	roleA := &RoleV3{
		Metadata: Metadata{Name: "roleA", Namespace: defaults.Namespace},
		Spec: RoleSpecV3{
			Options: RoleOptions{MaxSessionTTL: Duration(2 * time.Hour)},
			Allow: RoleConditions{
				Namespaces:    []string{defaults.Namespace},
				DatabaseNames: []string{"postgres", "main"},
				DatabaseUsers: []string{"postgres", "alice"},
			},
		},
	}
	roleB := &RoleV3{
		Metadata: Metadata{Name: "roleB", Namespace: defaults.Namespace},
		Spec: RoleSpecV3{
			Options: RoleOptions{MaxSessionTTL: Duration(time.Hour)},
			Allow: RoleConditions{
				Namespaces:    []string{defaults.Namespace},
				DatabaseNames: []string{"metrics"},
				DatabaseUsers: []string{"bob"},
			},
			Deny: RoleConditions{
				Namespaces:    []string{defaults.Namespace},
				DatabaseNames: []string{"postgres"},
				DatabaseUsers: []string{"postgres"},
			},
		},
	}
	testCases := []struct {
		name         string
		roles        []*RoleV3
		ttl          time.Duration
		overrideTTL  bool
		namesOut     []string
		usersOut     []string
		accessDenied bool
		notFound     bool
	}{
		{
			name:     "single role",
			roles:    []*RoleV3{roleA},
			ttl:      time.Hour,
			namesOut: []string{"postgres", "main"},
			usersOut: []string{"postgres", "alice"},
		},
		{
			name:     "combined roles",
			roles:    []*RoleV3{roleA, roleB},
			ttl:      time.Hour,
			namesOut: []string{"main", "metrics"},
			usersOut: []string{"alice", "bob"},
		},
		{
			name:         "ttl doesn't match",
			roles:        []*RoleV3{roleA},
			ttl:          5 * time.Hour,
			accessDenied: true,
		},
		{
			name:     "empty role",
			roles:    []*RoleV3{roleEmpty},
			ttl:      time.Hour,
			notFound: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var set RoleSet
			for _, r := range tc.roles {
				set = append(set, r)
			}
			names, users, err := set.CheckDatabaseNamesAndUsers(tc.ttl, tc.overrideTTL)
			if tc.accessDenied {
				require.Error(t, err)
				require.True(t, trace.IsAccessDenied(err))
			} else if tc.notFound {
				require.Error(t, err)
				require.True(t, trace.IsNotFound(err))
			} else {
				require.NoError(t, err)
				require.ElementsMatch(t, tc.namesOut, names)
				require.ElementsMatch(t, tc.usersOut, users)
			}
		})
	}
}

func TestCheckAccessToDatabaseService(t *testing.T) {
	utils.InitLoggerForTests(testing.Verbose())
	dbNoLabels := types.NewDatabaseServerV3("test",
		nil,
		types.DatabaseServerSpecV3{})
	dbStage := types.NewDatabaseServerV3("stage",
		map[string]string{"env": "stage"},
		types.DatabaseServerSpecV3{
			DynamicLabels: map[string]CommandLabelV2{"arch": {Result: "x86"}},
		})
	dbStage2 := types.NewDatabaseServerV3("stage2",
		map[string]string{"env": "stage"},
		types.DatabaseServerSpecV3{
			DynamicLabels: map[string]CommandLabelV2{"arch": {Result: "amd64"}},
		})
	dbProd := types.NewDatabaseServerV3("prod",
		map[string]string{"env": "prod"},
		types.DatabaseServerSpecV3{})
	roleAdmin := &RoleV3{
		Metadata: Metadata{Name: "admin", Namespace: defaults.Namespace},
		Spec: RoleSpecV3{
			Allow: RoleConditions{
				Namespaces:     []string{defaults.Namespace},
				DatabaseLabels: Labels{Wildcard: []string{Wildcard}},
			},
		},
	}
	roleDev := &RoleV3{
		Metadata: Metadata{Name: "dev", Namespace: defaults.Namespace},
		Spec: RoleSpecV3{
			Allow: RoleConditions{
				Namespaces:     []string{defaults.Namespace},
				DatabaseLabels: Labels{"env": []string{"stage"}},
			},
			Deny: RoleConditions{
				Namespaces:     []string{defaults.Namespace},
				DatabaseLabels: Labels{"arch": []string{"amd64"}},
			},
		},
	}
	roleIntern := &RoleV3{
		Metadata: Metadata{Name: "intern", Namespace: defaults.Namespace},
		Spec: RoleSpecV3{
			Allow: RoleConditions{
				Namespaces: []string{defaults.Namespace},
			},
		},
	}
	type access struct {
		server types.DatabaseServer
		access bool
	}
	testCases := []struct {
		name   string
		roles  []*RoleV3
		access []access
	}{
		{
			name:  "empty role doesn't have access to any databases",
			roles: nil,
			access: []access{
				{server: dbNoLabels, access: false},
				{server: dbStage, access: false},
				{server: dbStage2, access: false},
				{server: dbProd, access: false},
			},
		},
		{
			name:  "intern doesn't have access to any databases",
			roles: []*RoleV3{roleIntern},
			access: []access{
				{server: dbNoLabels, access: false},
				{server: dbStage, access: false},
				{server: dbStage2, access: false},
				{server: dbProd, access: false},
			},
		},
		{
			name:  "developer only has access to one of stage database",
			roles: []*RoleV3{roleDev},
			access: []access{
				{server: dbNoLabels, access: false},
				{server: dbStage, access: true},
				{server: dbStage2, access: false},
				{server: dbProd, access: false},
			},
		},
		{
			name:  "admin has access to all databases",
			roles: []*RoleV3{roleAdmin},
			access: []access{
				{server: dbNoLabels, access: true},
				{server: dbStage, access: true},
				{server: dbStage2, access: true},
				{server: dbProd, access: true},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var set RoleSet
			for _, r := range tc.roles {
				set = append(set, r)
			}
			for _, access := range tc.access {
				err := set.CheckAccessToDatabaseServer(access.server)
				if access.access {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
					require.True(t, trace.IsAccessDenied(err))
				}
			}
		})
	}
}

func TestCheckAccessToKubernetes(t *testing.T) {
	clusterNoLabels := &KubernetesCluster{
		Name: "no-labels",
	}
	clusterWithLabels := &KubernetesCluster{
		Name:          "no-labels",
		StaticLabels:  map[string]string{"foo": "bar"},
		DynamicLabels: map[string]CommandLabelV2{"baz": {Result: "qux"}},
	}
	wildcardRole := &RoleV3{
		Metadata: Metadata{
			Name:      "wildcard-labels",
			Namespace: defaults.Namespace,
		},
		Spec: RoleSpecV3{
			Allow: RoleConditions{
				Namespaces:       []string{defaults.Namespace},
				KubernetesLabels: Labels{Wildcard: []string{Wildcard}},
			},
		},
	}
	matchingLabelsRole := &RoleV3{
		Metadata: Metadata{
			Name:      "matching-labels",
			Namespace: defaults.Namespace,
		},
		Spec: RoleSpecV3{
			Allow: RoleConditions{
				Namespaces: []string{defaults.Namespace},
				KubernetesLabels: Labels{
					"foo": utils.Strings{"bar"},
					"baz": utils.Strings{"qux"},
				},
			},
		},
	}
	noLabelsRole := &RoleV3{
		Metadata: Metadata{
			Name:      "no-labels",
			Namespace: defaults.Namespace,
		},
		Spec: RoleSpecV3{
			Allow: RoleConditions{
				Namespaces: []string{defaults.Namespace},
			},
		},
	}
	mismatchingLabelsRole := &RoleV3{
		Metadata: Metadata{
			Name:      "mismatching-labels",
			Namespace: defaults.Namespace,
		},
		Spec: RoleSpecV3{
			Allow: RoleConditions{
				Namespaces: []string{defaults.Namespace},
				KubernetesLabels: Labels{
					"qux": utils.Strings{"baz"},
					"bar": utils.Strings{"foo"},
				},
			},
		},
	}
	testCases := []struct {
		name      string
		roles     []*RoleV3
		cluster   *KubernetesCluster
		hasAccess bool
	}{
		{
			name:      "empty role set has access to nothing",
			roles:     nil,
			cluster:   clusterNoLabels,
			hasAccess: false,
		},
		{
			name:      "role with no labels has access to nothing",
			roles:     []*RoleV3{noLabelsRole},
			cluster:   clusterNoLabels,
			hasAccess: false,
		},
		{
			name:      "role with wildcard labels matches cluster without labels",
			roles:     []*RoleV3{wildcardRole},
			cluster:   clusterNoLabels,
			hasAccess: true,
		},
		{
			name:      "role with wildcard labels matches cluster with labels",
			roles:     []*RoleV3{wildcardRole},
			cluster:   clusterWithLabels,
			hasAccess: true,
		},
		{
			name:      "role with labels does not match cluster with no labels",
			roles:     []*RoleV3{matchingLabelsRole},
			cluster:   clusterNoLabels,
			hasAccess: false,
		},
		{
			name:      "role with labels matches cluster with labels",
			roles:     []*RoleV3{matchingLabelsRole},
			cluster:   clusterWithLabels,
			hasAccess: true,
		},
		{
			name:      "role with mismatched labels does not match cluster with labels",
			roles:     []*RoleV3{mismatchingLabelsRole},
			cluster:   clusterWithLabels,
			hasAccess: false,
		},
		{
			name:      "one role in the roleset matches",
			roles:     []*RoleV3{mismatchingLabelsRole, noLabelsRole, matchingLabelsRole},
			cluster:   clusterWithLabels,
			hasAccess: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var set RoleSet
			for _, r := range tc.roles {
				set = append(set, r)
			}
			err := set.CheckAccessToKubernetes(defaults.Namespace, tc.cluster)
			if tc.hasAccess {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.True(t, trace.IsAccessDenied(err))
			}
		})
	}
}

// BenchmarkCheckAccessToServer tests how long it takes to run
// CheckAccessToServer across 4,000 nodes for 5 roles each with 5 logins each.
//
// To run benchmark:
//
//    go test -bench=.
//
// To run benchmark and obtain CPU and memory profiling:
//
//    go test -bench=. -cpuprofile=cpu.prof -memprofile=mem.prof
//
// To use the command line tool to read the profile:
//
//   go tool pprof cpu.prof
//   go tool pprof cpu.prof
//
// To generate a graph:
//
//   go tool pprof --pdf cpu.prof > cpu.pdf
//   go tool pprof --pdf mem.prof > mem.pdf
//
func BenchmarkCheckAccessToServer(b *testing.B) {
	servers := make([]*ServerV2, 0, 4000)

	// Create 4,000 servers with random IDs.
	for i := 0; i < 4000; i++ {
		hostname := uuid.NewUUID().String()
		servers = append(servers, &ServerV2{
			Kind:    KindNode,
			Version: V2,
			Metadata: Metadata{
				Name:      hostname,
				Namespace: defaults.Namespace,
			},
			Spec: ServerSpecV2{
				Addr:     "127.0.0.1:3022",
				Hostname: hostname,
			},
		})
	}

	// Create RoleSet with five roles: one admin role and four generic roles
	// that have five logins each and only have access to the foo:bar label.
	var set RoleSet
	set = append(set, NewAdminRole())
	for i := 0; i < 4; i++ {
		set = append(set, &RoleV3{
			Kind:    KindRole,
			Version: V3,
			Metadata: Metadata{
				Name:      strconv.Itoa(i),
				Namespace: defaults.Namespace,
			},
			Spec: RoleSpecV3{
				Allow: RoleConditions{
					Logins:     []string{"admin", "one", "two", "three", "four"},
					NodeLabels: Labels{"a": []string{"b"}},
				},
			},
		})
	}

	// Initialization is complete, start the benchmark timer.
	b.ResetTimer()

	// Build a map of all allowed logins.
	allowLogins := map[string]bool{}
	for _, role := range set {
		for _, login := range role.GetLogins(Allow) {
			allowLogins[login] = true
		}
	}

	// Check access to all 4,000 nodes.
	for n := 0; n < b.N; n++ {
		for i := 0; i < 4000; i++ {
			for login := range allowLogins {
				if err := set.CheckAccessToServer(login, servers[i]); err != nil {
					b.Error(err)
				}
			}
		}
	}
}

// userGetter is used in tests to return a user with the specified roles and
// traits.
type userGetter struct {
	roles  []string
	traits map[string][]string
}

func (f *userGetter) GetUser(name string, _ bool) (User, error) {
	user, err := NewUser(name)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	user.SetRoles(f.roles)
	user.SetTraits(f.traits)
	return user, nil
}
