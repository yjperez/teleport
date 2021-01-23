/*
Copyright 2020 Gravitational, Inc.

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

package types

import (
	"fmt"
	"time"

	"github.com/gravitational/teleport/api/constants"
	"github.com/gravitational/teleport/api/defaults"
	"github.com/gravitational/teleport/api/utils"

	"github.com/gravitational/trace"
)

// TrustedCluster holds information needed for a cluster that can not be directly
// accessed (maybe be behind firewall without any open ports) to join a parent cluster.
type TrustedCluster interface {
	// Resource provides common resource properties
	Resource
	// GetEnabled returns the state of the TrustedCluster.
	GetEnabled() bool
	// SetEnabled enables (handshake and add ca+reverse tunnel) or disables TrustedCluster.
	SetEnabled(bool)
	// CombinedMapping is used to specify combined mapping from legacy property Roles
	// and new property RoleMap
	CombinedMapping() RoleMap
	// GetRoleMap returns role map property
	GetRoleMap() RoleMap
	// SetRoleMap sets role map
	SetRoleMap(m RoleMap)
	// GetRoles returns the roles for the certificate authority.
	GetRoles() []string
	// SetRoles sets the roles for the certificate authority.
	SetRoles([]string)
	// GetToken returns the authorization and authentication token.
	GetToken() string
	// SetToken sets the authorization and authentication.
	SetToken(string)
	// GetProxyAddress returns the address of the proxy server.
	GetProxyAddress() string
	// SetProxyAddress sets the address of the proxy server.
	SetProxyAddress(string)
	// GetReverseTunnelAddress returns the address of the reverse tunnel.
	GetReverseTunnelAddress() string
	// SetReverseTunnelAddress sets the address of the reverse tunnel.
	SetReverseTunnelAddress(string)
	// CheckAndSetDefaults checks and set default values for missing fields.
	CheckAndSetDefaults() error
	// CanChangeStateTo checks the TrustedCluster can transform into another.
	CanChangeStateTo(TrustedCluster) error
}

// NewTrustedCluster is a convenience way to create a TrustedCluster resource.
func NewTrustedCluster(name string, spec TrustedClusterSpecV2) (TrustedCluster, error) {
	return &TrustedClusterV2{
		Kind:    KindTrustedCluster,
		Version: V2,
		Metadata: Metadata{
			Name:      name,
			Namespace: defaults.Namespace,
		},
		Spec: spec,
	}, nil
}

// TrustedClusterV2 implements TrustedCluster.
type TrustedClusterV2 struct {
	// Kind is a resource kind - always resource.
	Kind string `json:"kind"`

	// SubKind is a resource sub kind
	SubKind string `json:"sub_kind,omitempty"`

	// Version is a resource version.
	Version string `json:"version"`

	// Metadata is metadata about the resource.
	Metadata Metadata `json:"metadata"`

	// Spec is the specification of the resource.
	Spec TrustedClusterSpecV2 `json:"spec"`
}

// TrustedClusterSpecV2 is the actual data we care about for TrustedClusterSpecV2.
type TrustedClusterSpecV2 struct {
	// Enabled is a bool that indicates if the TrustedCluster is enabled or disabled.
	// Setting Enabled to false has a side effect of deleting the user and host
	// certificate authority (CA).
	Enabled bool `json:"enabled"`

	// Roles is a list of roles that users will be assuming when connecting to this cluster.
	Roles []string `json:"roles,omitempty"`

	// Token is the authorization token provided by another cluster needed by
	// this cluster to join.
	Token string `json:"token"`

	// ProxyAddress is the address of the web proxy server of the cluster to join. If not set,
	// it is derived from <metadata.name>:<default web proxy server port>.
	ProxyAddress string `json:"web_proxy_addr"`

	// ReverseTunnelAddress is the address of the SSH proxy server of the cluster to join. If
	// not set, it is derived from <metadata.name>:<default reverse tunnel port>.
	ReverseTunnelAddress string `json:"tunnel_addr"`

	// RoleMap specifies role mappings to remote roles
	RoleMap RoleMap `json:"role_map,omitempty"`
}

// CheckAndSetDefaults checks validity of all parameters and sets defaults
func (c *TrustedClusterV2) CheckAndSetDefaults() error {
	// make sure we have defaults for all fields
	if err := c.Metadata.CheckAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}
	// This is to force users to migrate
	if len(c.Spec.Roles) != 0 && len(c.Spec.RoleMap) != 0 {
		return trace.BadParameter("should set either 'roles' or 'role_map', not both")
	}
	// Imply that by default proxy listens on the same port for
	// web and reverse tunnel connections
	if c.Spec.ReverseTunnelAddress == "" {
		c.Spec.ReverseTunnelAddress = c.Spec.ProxyAddress
	}
	if err := c.Spec.RoleMap.Check(); err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// GetVersion returns resource version
func (c *TrustedClusterV2) GetVersion() string {
	return c.Version
}

// GetKind returns resource kind
func (c *TrustedClusterV2) GetKind() string {
	return c.Kind
}

// GetSubKind returns resource sub kind
func (c *TrustedClusterV2) GetSubKind() string {
	return c.SubKind
}

// SetSubKind sets resource subkind
func (c *TrustedClusterV2) SetSubKind(s string) {
	c.SubKind = s
}

// GetResourceID returns resource ID
func (c *TrustedClusterV2) GetResourceID() int64 {
	return c.Metadata.ID
}

// SetResourceID sets resource ID
func (c *TrustedClusterV2) SetResourceID(id int64) {
	c.Metadata.ID = id
}

// CombinedMapping is used to specify combined mapping from legacy property Roles
// and new property RoleMap
func (c *TrustedClusterV2) CombinedMapping() RoleMap {
	if len(c.Spec.Roles) != 0 {
		return []RoleMapping{{Remote: Wildcard, Local: c.Spec.Roles}}
	}
	return c.Spec.RoleMap
}

// GetRoleMap returns role map property
func (c *TrustedClusterV2) GetRoleMap() RoleMap {
	return c.Spec.RoleMap
}

// SetRoleMap sets role map
func (c *TrustedClusterV2) SetRoleMap(m RoleMap) {
	c.Spec.RoleMap = m
}

// GetMetadata returns object metadata
func (c *TrustedClusterV2) GetMetadata() Metadata {
	return c.Metadata
}

// SetExpiry sets expiry time for the object
func (c *TrustedClusterV2) SetExpiry(expires time.Time) {
	c.Metadata.SetExpiry(expires)
}

// Expiry returns object expiry setting
func (c *TrustedClusterV2) Expiry() time.Time {
	return c.Metadata.Expiry()
}

// SetTTL sets Expires header using the provided clock.
// Use SetExpiry instead.
// DELETE IN 7.0.0
func (c *TrustedClusterV2) SetTTL(clock Clock, ttl time.Duration) {
	c.Metadata.SetTTL(clock, ttl)
}

// GetName returns the name of the TrustedCluster.
func (c *TrustedClusterV2) GetName() string {
	return c.Metadata.Name
}

// SetName sets the name of the TrustedCluster.
func (c *TrustedClusterV2) SetName(e string) {
	c.Metadata.Name = e
}

// GetEnabled returns the state of the TrustedCluster.
func (c *TrustedClusterV2) GetEnabled() bool {
	return c.Spec.Enabled
}

// SetEnabled enables (handshake and add ca+reverse tunnel) or disables TrustedCluster.
func (c *TrustedClusterV2) SetEnabled(e bool) {
	c.Spec.Enabled = e
}

// GetRoles returns the roles for the certificate authority.
func (c *TrustedClusterV2) GetRoles() []string {
	return c.Spec.Roles
}

// SetRoles sets the roles for the certificate authority.
func (c *TrustedClusterV2) SetRoles(e []string) {
	c.Spec.Roles = e
}

// GetToken returns the authorization and authentication token.
func (c *TrustedClusterV2) GetToken() string {
	return c.Spec.Token
}

// SetToken sets the authorization and authentication.
func (c *TrustedClusterV2) SetToken(e string) {
	c.Spec.Token = e
}

// GetProxyAddress returns the address of the proxy server.
func (c *TrustedClusterV2) GetProxyAddress() string {
	return c.Spec.ProxyAddress
}

// SetProxyAddress sets the address of the proxy server.
func (c *TrustedClusterV2) SetProxyAddress(e string) {
	c.Spec.ProxyAddress = e
}

// GetReverseTunnelAddress returns the address of the reverse tunnel.
func (c *TrustedClusterV2) GetReverseTunnelAddress() string {
	return c.Spec.ReverseTunnelAddress
}

// SetReverseTunnelAddress sets the address of the reverse tunnel.
func (c *TrustedClusterV2) SetReverseTunnelAddress(e string) {
	c.Spec.ReverseTunnelAddress = e
}

// CanChangeStateTo checks if the state change is allowed or not. If not, returns
// an error explaining the reason.
func (c *TrustedClusterV2) CanChangeStateTo(t TrustedCluster) error {
	immutableFieldErr := func(name string) error {
		return trace.BadParameter("can not update %s for existing leaf cluster, delete and re-create this leaf cluster with updated %s", name, name)
	}
	if c.GetToken() != t.GetToken() {
		return immutableFieldErr("token")
	}
	if c.GetProxyAddress() != t.GetProxyAddress() {
		return immutableFieldErr("web_proxy_address")
	}
	if c.GetReverseTunnelAddress() != t.GetReverseTunnelAddress() {
		return immutableFieldErr("tunnel_addr")
	}
	if !utils.StringSlicesEqual(c.GetRoles(), t.GetRoles()) {
		return immutableFieldErr("roles")
	}
	if !c.GetRoleMap().Equals(t.GetRoleMap()) {
		return immutableFieldErr("role_map")
	}

	if c.GetEnabled() == t.GetEnabled() {
		if t.GetEnabled() {
			return trace.AlreadyExists("leaf cluster is already enabled, this update would have no effect")
		}
		return trace.AlreadyExists("leaf cluster is already disabled, this update would have no effect")
	}

	return nil
}

// String represents a human readable version of trusted cluster settings.
func (c *TrustedClusterV2) String() string {
	return fmt.Sprintf("TrustedCluster(Enabled=%v,Roles=%v,Token=%v,ProxyAddress=%v,ReverseTunnelAddress=%v)",
		c.Spec.Enabled, c.Spec.Roles, c.Spec.Token, c.Spec.ProxyAddress, c.Spec.ReverseTunnelAddress)
}

// Equals checks if the two role mappings are equal.
func (r RoleMapping) Equals(o RoleMapping) bool {
	if r.Remote != o.Remote {
		return false
	}
	if !utils.StringSlicesEqual(r.Local, r.Local) {
		return false
	}
	return true
}

// RoleMap is a list of mappings
type RoleMap []RoleMapping

// Equals checks if the two role maps are equal.
func (r RoleMap) Equals(o RoleMap) bool {
	if len(r) != len(o) {
		return false
	}
	for i := range r {
		if !r[i].Equals(o[i]) {
			return false
		}
	}
	return true
}

// String prints user friendly representation of role mapping
func (r RoleMap) String() string {
	values, err := r.parse()
	if err != nil {
		return fmt.Sprintf("<failed to parse: %v", err)
	}
	if len(values) != 0 {
		return fmt.Sprintf("%v", values)
	}
	return "<empty>"
}

func (r RoleMap) parse() (map[string][]string, error) {
	directMatch := make(map[string][]string)
	for i := range r {
		roleMap := r[i]
		if roleMap.Remote == "" {
			return nil, trace.BadParameter("missing 'remote' parameter for role_map")
		}
		_, err := utils.ReplaceRegexp(roleMap.Remote, "", "")
		if trace.IsBadParameter(err) {
			return nil, trace.BadParameter("failed to parse 'remote' parameter for role_map: %v", err.Error())
		}
		if len(roleMap.Local) == 0 {
			return nil, trace.BadParameter("missing 'local' parameter for 'role_map'")
		}
		for _, local := range roleMap.Local {
			if local == "" {
				return nil, trace.BadParameter("missing 'local' property of 'role_map' entry")
			}
			if local == Wildcard {
				return nil, trace.BadParameter("wildcard value is not supported for 'local' property of 'role_map' entry")
			}
		}
		_, ok := directMatch[roleMap.Remote]
		if ok {
			return nil, trace.BadParameter("remote role '%v' match is already specified", roleMap.Remote)
		}
		directMatch[roleMap.Remote] = roleMap.Local
	}
	return directMatch, nil
}

// Map maps local roles to remote roles
func (r RoleMap) Map(remoteRoles []string) ([]string, error) {
	_, err := r.parse()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	var outRoles []string
	// when no remote roles are specified, assume that
	// there is a single empty remote role (that should match wildcards)
	if len(remoteRoles) == 0 {
		remoteRoles = []string{""}
	}
	for _, mapping := range r {
		expression := mapping.Remote
		for _, remoteRole := range remoteRoles {
			// never map default implicit role, it is always
			// added by default
			if remoteRole == constants.DefaultImplicitRole {
				continue
			}
			for _, replacementRole := range mapping.Local {
				replacement, err := utils.ReplaceRegexp(expression, replacementRole, remoteRole)
				switch {
				case err == nil:
					// empty replacement can occur when $2 expand refers
					// to non-existing capture group in match expression
					if replacement != "" {
						outRoles = append(outRoles, replacement)
					}
				case trace.IsNotFound(err):
					continue
				default:
					return nil, trace.Wrap(err)
				}
			}
		}
	}
	return outRoles, nil
}

// Check checks RoleMap for errors
func (r RoleMap) Check() error {
	_, err := r.parse()
	return trace.Wrap(err)
}

// SortedTrustedCluster sorts clusters by name
type SortedTrustedCluster []TrustedCluster

// Len returns the length of a list.
func (s SortedTrustedCluster) Len() int {
	return len(s)
}

// Less compares items by name.
func (s SortedTrustedCluster) Less(i, j int) bool {
	return s[i].GetName() < s[j].GetName()
}

// Swap swaps two items in a list.
func (s SortedTrustedCluster) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
