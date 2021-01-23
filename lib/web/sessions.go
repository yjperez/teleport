/*
Copyright 2015-2019 Gravitational, Inc.

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

package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/gravitational/teleport"
	apiclient "github.com/gravitational/teleport/api/client"
	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/auth"
	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/reversetunnel"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/services/local"
	"github.com/gravitational/teleport/lib/tlsca"
	"github.com/gravitational/teleport/lib/utils"

	"github.com/gravitational/trace"

	"github.com/jonboulle/clockwork"
	"github.com/sirupsen/logrus"
	"github.com/tstranex/u2f"
)

// SessionContext is a context associated with a user's
// web session. An instance of the context is created for
// each web session generated for the user and provides
// a basic client cache for remote auth server connections.
type SessionContext struct {
	log    logrus.FieldLogger
	user   string
	clt    auth.ClientI
	parent *sessionCache
	// resources is persistent resource store this context is bound to.
	// The store maintains a list of resources between session renewals
	resources *sessionResources
	// session refers the web session created for the user.
	session services.WebSession

	mu        sync.Mutex
	remoteClt map[string]auth.ClientI
}

// String returns the text representation of this context
func (c *SessionContext) String() string {
	return fmt.Sprintf("WebSession(user=%v,id=%v,expires=%v,bearer=%v,bearer_expires=%v)",
		c.user,
		c.session.GetName(),
		c.session.GetExpiryTime(),
		c.session.GetBearerToken(),
		c.session.GetBearerTokenExpiryTime(),
	)
}

// AddClosers adds the specified closers to this context
func (c *SessionContext) AddClosers(closers ...io.Closer) {
	c.resources.addClosers(closers...)
}

// RemoveCloser removes the specified closer from this context
func (c *SessionContext) RemoveCloser(closer io.Closer) {
	c.resources.removeCloser(closer)
}

// Invalidate invalidates this context by removing the underlying session
// and closing all underlying closers
func (c *SessionContext) Invalidate() error {
	return c.parent.invalidateSession(c)
}

func (c *SessionContext) validateBearerToken(ctx context.Context, token string) error {
	_, err := c.parent.readBearerToken(ctx, types.GetWebTokenRequest{
		User:  c.user,
		Token: token,
	})
	return trace.Wrap(err)
}

func (c *SessionContext) addRemoteClient(siteName string, remoteClient auth.ClientI) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remoteClt[siteName] = remoteClient
}

func (c *SessionContext) getRemoteClient(siteName string) (auth.ClientI, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	remoteClt, ok := c.remoteClt[siteName]
	return remoteClt, ok
}

// GetClient returns the client connected to the auth server
func (c *SessionContext) GetClient() (auth.ClientI, error) {
	return c.clt, nil
}

// GetUserClient will return an auth.ClientI with the role of the user at
// the requested site. If the site is local a client with the users local role
// is returned. If the site is remote a client with the users remote role is
// returned.
func (c *SessionContext) GetUserClient(site reversetunnel.RemoteSite) (auth.ClientI, error) {
	// get the name of the current cluster
	clusterName, err := c.clt.GetClusterName()
	if err != nil {
		return nil, trace.Wrap(err)
	}

	// if we're trying to access the local cluster, pass back the local client.
	if clusterName.GetClusterName() == site.GetName() {
		return c.clt, nil
	}

	// check if we already have a connection to this cluster
	remoteClt, ok := c.getRemoteClient(site.GetName())
	if !ok {
		rClt, err := c.newRemoteClient(site)
		if err != nil {
			return nil, trace.Wrap(err)
		}

		// we'll save the remote client in our session context so we don't have to
		// build a new connection next time. all remote clients will be closed when
		// the session context is closed.
		c.addRemoteClient(site.GetName(), rClt)

		return rClt, nil
	}

	return remoteClt, nil
}

// newRemoteClient returns a client to a remote cluster with the role of
// the logged in user.
func (c *SessionContext) newRemoteClient(cluster reversetunnel.RemoteSite) (auth.ClientI, error) {
	clt, err := c.tryRemoteTLSClient(cluster)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return clt, nil
}

// clusterDialer returns DialContext function using cluster's dial function
func clusterDialer(remoteCluster reversetunnel.RemoteSite) auth.ContextDialer {
	return auth.ContextDialerFunc(func(in context.Context, network, _ string) (net.Conn, error) {
		return remoteCluster.DialAuthServer()
	})
}

// tryRemoteTLSClient tries creating TLS client and using it (the client may not be available
// due to older clusters), returns client if it is working properly
func (c *SessionContext) tryRemoteTLSClient(cluster reversetunnel.RemoteSite) (auth.ClientI, error) {
	clt, err := c.newRemoteTLSClient(cluster)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	_, err = clt.GetDomainName()
	if err != nil {
		return clt, trace.Wrap(err)
	}
	return clt, nil
}

// ClientTLSConfig returns client TLS authentication associated
// with the web session context
func (c *SessionContext) ClientTLSConfig(clusterName ...string) (*tls.Config, error) {
	var certPool *x509.CertPool
	if len(clusterName) == 0 {
		certAuthorities, err := c.parent.proxyClient.GetCertAuthorities(services.HostCA, false)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		certPool, err = services.CertPoolFromCertAuthorities(certAuthorities)
		if err != nil {
			return nil, trace.Wrap(err)
		}
	} else {
		certAuthority, err := c.parent.proxyClient.GetCertAuthority(services.CertAuthID{
			Type:       services.HostCA,
			DomainName: clusterName[0],
		}, false)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		certPool, err = services.CertPool(certAuthority)
		if err != nil {
			return nil, trace.Wrap(err)
		}
	}

	tlsConfig := utils.TLSConfig(c.parent.cipherSuites)
	tlsCert, err := tls.X509KeyPair(c.session.GetTLSCert(), c.session.GetPriv())
	if err != nil {
		return nil, trace.Wrap(err, "failed to parse TLS cert and key")
	}
	tlsConfig.Certificates = []tls.Certificate{tlsCert}
	tlsConfig.RootCAs = certPool
	tlsConfig.ServerName = auth.EncodeClusterName(c.parent.clusterName)
	tlsConfig.Time = c.parent.clock.Now
	return tlsConfig, nil
}

func (c *SessionContext) newRemoteTLSClient(cluster reversetunnel.RemoteSite) (auth.ClientI, error) {
	tlsConfig, err := c.ClientTLSConfig(cluster.GetName())
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return auth.NewClient(apiclient.Config{Dialer: clusterDialer(cluster), TLS: tlsConfig})
}

// GetUser returns the authenticated teleport user
func (c *SessionContext) GetUser() string {
	return c.user
}

// extendWebSession creates a new web session for this user
// based on the previous session
func (c *SessionContext) extendWebSession(accessRequestID string) (services.WebSession, error) {
	session, err := c.clt.ExtendWebSession(c.user, c.session.GetName(), accessRequestID)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return session, nil
}

// GetAgent returns agent that can be used to answer challenges
// for the web to ssh connection as well as certificate
func (c *SessionContext) GetAgent() (agent.Agent, *ssh.Certificate, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey(c.session.GetPub())
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil, nil, trace.BadParameter("expected certificate, got %T", pub)
	}
	if len(cert.ValidPrincipals) == 0 {
		return nil, nil, trace.BadParameter("expected at least valid principal in certificate")
	}
	privateKey, err := ssh.ParseRawPrivateKey(c.session.GetPriv())
	if err != nil {
		return nil, nil, trace.Wrap(err, "failed to parse SSH private key")
	}

	keyring := agent.NewKeyring()
	err = keyring.Add(agent.AddedKey{
		PrivateKey:  privateKey,
		Certificate: cert,
	})
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	return keyring, cert, nil
}

// GetCertificates returns the *ssh.Certificate and *x509.Certificate
// associated with this context's session.
func (c *SessionContext) GetCertificates() (*ssh.Certificate, *x509.Certificate, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey(c.session.GetPub())
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	sshCert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil, nil, trace.BadParameter("not certificate")
	}
	tlsCert, err := tlsca.ParseCertificatePEM(c.session.GetTLSCert())
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	return sshCert, tlsCert, nil

}

// GetSessionID returns the ID of the underlying user web session.
func (c *SessionContext) GetSessionID() string {
	return c.session.GetName()
}

// Close cleans up resources associated with this context and removes it
// from the user context
func (c *SessionContext) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var errors []error
	for _, clt := range c.remoteClt {
		if err := clt.Close(); err != nil {
			errors = append(errors, err)
		}
	}
	if err := c.clt.Close(); err != nil {
		errors = append(errors, err)
	}
	return trace.NewAggregate(errors...)
}

// getToken returns the bearer token associated with the underlying
// session. Note that sessions are separate from bearer tokens and this
// is only useful immediately after a session has been created to query
// the token.
func (c *SessionContext) getToken() types.WebToken {
	return types.NewWebToken(types.WebTokenSpecV3{
		Token:   c.session.GetBearerToken(),
		Expires: c.session.GetBearerTokenExpiryTime(),
	})
}

// expired returns whether this context has expired.
// The context is considered expired when its bearer token TTL
// is in the past
func (c *SessionContext) expired(ctx context.Context) bool {
	_, err := c.parent.readSession(ctx, types.GetWebSessionRequest{
		User:      c.user,
		SessionID: c.session.GetName(),
	})
	if err == nil {
		return false
	}
	c.log.WithError(err).Warn("Failed to query web session.")
	expiry := c.session.Expiry()
	if expiry.IsZero() {
		return false
	}
	// Give the session some time to linger so existing users of the context
	// have successfully disposed of them.
	// If we remove the session immediately, a stale copy might still use the
	// cached site clients.
	// This is a cheaper way to avoid race without introducing object
	// reference counters.
	// TODO(dmitri): this leaks the implementation detail about the session TTL
	return c.parent.clock.Since(expiry) > 2*auth.BearerTokenTTL
}

type sessionCacheOptions struct {
	proxyClient  auth.ClientI
	accessPoint  auth.ReadAccessPoint
	servers      []utils.NetAddr
	cipherSuites []uint16
	clock        clockwork.Clock
}

// newSessionCache returns new instance of the session cache
func newSessionCache(config sessionCacheOptions) (*sessionCache, error) {
	clusterName, err := config.proxyClient.GetClusterName()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if config.clock == nil {
		config.clock = clockwork.NewRealClock()
	}
	cache := &sessionCache{
		clusterName:  clusterName.GetClusterName(),
		proxyClient:  config.proxyClient,
		accessPoint:  config.accessPoint,
		sessions:     make(map[string]*SessionContext),
		resources:    make(map[string]*sessionResources),
		authServers:  config.servers,
		closer:       utils.NewCloseBroadcaster(),
		cipherSuites: config.cipherSuites,
		log:          newPackageLogger(),
		clock:        config.clock,
	}
	// periodically close expired and unused sessions
	go cache.expireSessions()
	return cache, nil
}

// sessionCache handles web session authentication,
// and holds in-memory contexts associated with each session
type sessionCache struct {
	log         logrus.FieldLogger
	proxyClient auth.ClientI
	authServers []utils.NetAddr
	accessPoint auth.ReadAccessPoint
	closer      *utils.CloseBroadcaster
	clusterName string
	clock       clockwork.Clock
	// cipherSuites is the list of supported TLS cipher suites.
	cipherSuites []uint16

	mu sync.Mutex
	// sessions maps user/sessionID to an active web session value between renewals.
	// This is the client-facing session handle
	sessions map[string]*SessionContext

	// session cache maintains a list of resources per-user as long
	// as the user session is active even though individual session values
	// are periodically recycled.
	// Resources are disposed of when the corresponding session
	// is either explicitly invalidated (e.g. during logout) or the
	// resources are themselves closing
	resources map[string]*sessionResources
}

// Close closes all allocated resources and stops goroutines
func (s *sessionCache) Close() error {
	s.log.Info("Closing session cache.")
	return s.closer.Close()
}

func (s *sessionCache) expireSessions() {
	ticker := s.clock.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.Chan():
			s.clearExpiredSessions(context.TODO())
		case <-s.closer.C:
			return
		}
	}
}

func (s *sessionCache) clearExpiredSessions(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.sessions {
		if !c.expired(ctx) {
			continue
		}
		s.removeSessionContextLocked(c.session.GetUser(), c.session.GetName())
		s.log.WithField("ctx", c.String()).Debug("Context expired.")
	}
}

// AuthWithOTP authenticates the specified user with the given password and OTP token.
// Returns a new web session if successful.
func (s *sessionCache) AuthWithOTP(user, pass, otpToken string) (services.WebSession, error) {
	return s.proxyClient.AuthenticateWebUser(auth.AuthenticateUserRequest{
		Username: user,
		OTP: &auth.OTPCreds{
			Password: []byte(pass),
			Token:    otpToken,
		},
	})
}

// AuthWithoutOTP authenticates the specified user with the given password.
// Returns a new web session if successful.
func (s *sessionCache) AuthWithoutOTP(user, pass string) (services.WebSession, error) {
	return s.proxyClient.AuthenticateWebUser(auth.AuthenticateUserRequest{
		Username: user,
		Pass: &auth.PassCreds{
			Password: []byte(pass),
		},
	})
}

// GetU2FSignRequest creates a new U2F sign request for the specified user with given
// password.
func (s *sessionCache) GetU2FSignRequest(user, pass string) (*u2f.SignRequest, error) {
	return s.proxyClient.GetU2FSignRequest(user, []byte(pass))
}

// AuthWithU2FSignResponse authenticates the specified user with the given U2F response.
// Returns a new web session if successful.
func (s *sessionCache) AuthWithU2FSignResponse(user string, response *u2f.SignResponse) (services.WebSession, error) {
	return s.proxyClient.AuthenticateWebUser(auth.AuthenticateUserRequest{
		Username: user,
		U2F: &auth.U2FSignResponseCreds{
			SignResponse: *response,
		},
	})
}

// GetCertificateWithoutOTP returns a new user certificate for the specified request.
func (s *sessionCache) GetCertificateWithoutOTP(c client.CreateSSHCertReq) (*auth.SSHLoginResponse, error) {
	return s.proxyClient.AuthenticateSSHUser(auth.AuthenticateSSHRequest{
		AuthenticateUserRequest: auth.AuthenticateUserRequest{
			Username: c.User,
			Pass: &auth.PassCreds{
				Password: []byte(c.Password),
			},
		},
		PublicKey:         c.PubKey,
		CompatibilityMode: c.Compatibility,
		TTL:               c.TTL,
		RouteToCluster:    c.RouteToCluster,
		KubernetesCluster: c.KubernetesCluster,
	})
}

// GetCertificateWithOTP returns a new user certificate for the specified request.
// The request is used with the given OTP token.
func (s *sessionCache) GetCertificateWithOTP(c client.CreateSSHCertReq) (*auth.SSHLoginResponse, error) {
	return s.proxyClient.AuthenticateSSHUser(auth.AuthenticateSSHRequest{
		AuthenticateUserRequest: auth.AuthenticateUserRequest{
			Username: c.User,
			OTP: &auth.OTPCreds{
				Password: []byte(c.Password),
				Token:    c.OTPToken,
			},
		},
		PublicKey:         c.PubKey,
		CompatibilityMode: c.Compatibility,
		TTL:               c.TTL,
		RouteToCluster:    c.RouteToCluster,
		KubernetesCluster: c.KubernetesCluster,
	})
}

func (s *sessionCache) GetCertificateWithU2F(c client.CreateSSHCertWithU2FReq) (*auth.SSHLoginResponse, error) {
	return s.proxyClient.AuthenticateSSHUser(auth.AuthenticateSSHRequest{
		AuthenticateUserRequest: auth.AuthenticateUserRequest{
			Username: c.User,
			U2F: &auth.U2FSignResponseCreds{
				SignResponse: c.U2FSignResponse,
			},
		},
		PublicKey:         c.PubKey,
		CompatibilityMode: c.Compatibility,
		TTL:               c.TTL,
		RouteToCluster:    c.RouteToCluster,
		KubernetesCluster: c.KubernetesCluster,
	})
}

// Ping gets basic info about the auth server.
func (s *sessionCache) Ping(ctx context.Context) (proto.PingResponse, error) {
	return s.proxyClient.Ping(ctx)
}

func (s *sessionCache) GetUserInviteU2FRegisterRequest(token string) (*u2f.RegisterRequest, error) {
	return s.proxyClient.GetSignupU2FRegisterRequest(token)
}

func (s *sessionCache) ValidateTrustedCluster(validateRequest *auth.ValidateTrustedClusterRequest) (*auth.ValidateTrustedClusterResponse, error) {
	return s.proxyClient.ValidateTrustedCluster(validateRequest)
}

// validateSession validates the session given with user and session ID.
// Returns a new or existing session context.
func (s *sessionCache) validateSession(ctx context.Context, user, sessionID string) (*SessionContext, error) {
	sessionCtx, err := s.getContext(user, sessionID)
	if err == nil {
		return sessionCtx, nil
	}
	if !trace.IsNotFound(err) {
		return nil, trace.Wrap(err)
	}
	return s.newSessionContext(user, sessionID)
}

func (s *sessionCache) invalidateSession(ctx *SessionContext) error {
	defer ctx.Close()
	clt, err := ctx.GetClient()
	if err != nil {
		return trace.Wrap(err)
	}
	// Delete just the session - leave the bearer token to linger to avoid
	// failing a client query still using the old token.
	err = clt.WebSessions().Delete(context.TODO(), types.DeleteWebSessionRequest{
		User:      ctx.user,
		SessionID: ctx.session.GetName(),
	})
	if err != nil && !trace.IsNotFound(err) {
		return trace.Wrap(err)
	}
	if err := s.releaseResources(ctx.GetUser(), ctx.session.GetName()); err != nil {
		return trace.Wrap(err)
	}
	return nil
}

func (s *sessionCache) getContext(user, sessionID string) (*SessionContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, ok := s.sessions[user+sessionID]
	if ok {
		return ctx, nil
	}
	return nil, trace.NotFound("no context for user %v and session %v",
		user, sessionID)
}

func (s *sessionCache) insertContext(user string, ctx *SessionContext) (exists bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := sessionKey(user, ctx.session.GetName())
	if _, exists := s.sessions[id]; exists {
		return true
	}
	s.sessions[id] = ctx
	return false
}

func (s *sessionCache) releaseResources(user, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releaseResourcesLocked(user, sessionID)
}

func (s *sessionCache) removeSessionContextLocked(user, sessionID string) error {
	id := sessionKey(user, sessionID)
	ctx, ok := s.sessions[id]
	if !ok {
		return nil
	}
	delete(s.sessions, id)
	err := ctx.Close()
	if err != nil {
		s.log.WithFields(logrus.Fields{
			"ctx":           ctx.String(),
			logrus.ErrorKey: err,
		}).Warn("Failed to close session context.")
		return trace.Wrap(err)
	}
	return nil
}

func (s *sessionCache) releaseResourcesLocked(user, sessionID string) error {
	var errors []error
	err := s.removeSessionContextLocked(user, sessionID)
	if err != nil {
		errors = append(errors, err)
	}
	if ctx, ok := s.resources[user]; ok {
		delete(s.resources, user)
		if err := ctx.Close(); err != nil {
			s.log.WithError(err).Warn("Failed to clean up session context.")
			errors = append(errors, err)
		}
	}
	return trace.NewAggregate(errors...)
}

func (s *sessionCache) upsertSessionContext(user string) *sessionResources {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx, exists := s.resources[user]; exists {
		return ctx
	}
	ctx := &sessionResources{
		log: s.log.WithFields(logrus.Fields{
			trace.Component: "user-session",
			"user":          user,
		}),
	}
	s.resources[user] = ctx
	return ctx
}

// newSessionContext creates a new web session context for the specified user/session ID
func (s *sessionCache) newSessionContext(user, sessionID string) (*SessionContext, error) {
	session, err := s.proxyClient.AuthenticateWebUser(auth.AuthenticateUserRequest{
		Username: user,
		Session: &auth.SessionCreds{
			ID: sessionID,
		},
	})
	if err != nil {
		// This will fail if the session has expired and was removed
		return nil, trace.Wrap(err)
	}
	return s.newSessionContextFromSession(session)
}

func (s *sessionCache) newSessionContextFromSession(session services.WebSession) (*SessionContext, error) {
	tlsConfig, err := s.tlsConfig(session.GetTLSCert(), session.GetPriv())
	if err != nil {
		return nil, trace.Wrap(err)
	}
	userClient, err := auth.NewTLSClient(auth.ClientConfig{
		Addrs: s.authServers,
		TLS:   tlsConfig,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	ctx := &SessionContext{
		clt:       userClient,
		remoteClt: make(map[string]auth.ClientI),
		user:      session.GetUser(),
		session:   session,
		parent:    s,
		resources: s.upsertSessionContext(session.GetUser()),
		log: s.log.WithFields(logrus.Fields{
			"user":    session.GetUser(),
			"session": session.GetShortName(),
		}),
	}
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if exists := s.insertContext(session.GetUser(), ctx); exists {
		// this means that someone has just inserted the context, so
		// close our extra context and return
		ctx.Close()
	}
	return ctx, nil
}

func (s *sessionCache) tlsConfig(cert, privKey []byte) (*tls.Config, error) {
	ca, err := s.proxyClient.GetCertAuthority(services.CertAuthID{
		Type:       services.HostCA,
		DomainName: s.clusterName,
	}, false)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	certPool, err := services.CertPool(ca)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	tlsConfig := utils.TLSConfig(s.cipherSuites)
	tlsCert, err := tls.X509KeyPair(cert, privKey)
	if err != nil {
		return nil, trace.Wrap(err, "failed to parse TLS certificate and key")
	}
	tlsConfig.Certificates = []tls.Certificate{tlsCert}
	tlsConfig.RootCAs = certPool
	tlsConfig.ServerName = auth.EncodeClusterName(s.clusterName)
	tlsConfig.Time = s.clock.Now
	return tlsConfig, nil
}

func (s *sessionCache) readSession(ctx context.Context, req types.GetWebSessionRequest) (types.WebSession, error) {
	// Read session from the cache first
	session, err := s.accessPoint.GetWebSession(ctx, req)
	if err == nil {
		return session, nil
	}
	// Fallback to proxy otherwise
	return s.proxyClient.GetWebSession(ctx, req)
}

func (s *sessionCache) readBearerToken(ctx context.Context, req types.GetWebTokenRequest) (types.WebToken, error) {
	// Read token from the cache first
	token, err := s.accessPoint.GetWebToken(ctx, req)
	if err == nil {
		return token, nil
	}
	// Fallback to proxy otherwise
	return s.proxyClient.GetWebToken(ctx, req)
}

// Close releases all underlying resources for the user session.
func (c *sessionResources) Close() error {
	closers := c.transferClosers()
	var errors []error
	for _, closer := range closers {
		c.log.Debugf("Closing %v.", closer)
		if err := closer.Close(); err != nil {
			errors = append(errors, err)
		}
	}
	return trace.NewAggregate(errors...)
}

// sessionResources persists resources initiated by a web session
// but which might outlive the session.
type sessionResources struct {
	log logrus.FieldLogger

	mu      sync.Mutex
	closers []io.Closer
}

// addClosers adds the specified closers to this context
func (c *sessionResources) addClosers(closers ...io.Closer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closers = append(c.closers, closers...)
}

// removeCloser removes the specified closer from this context
func (c *sessionResources) removeCloser(closer io.Closer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, cls := range c.closers {
		if cls == closer {
			c.closers = append(c.closers[:i], c.closers[i+1:]...)
			return
		}
	}
}

func (c *sessionResources) transferClosers() []io.Closer {
	c.mu.Lock()
	defer c.mu.Unlock()
	closers := c.closers
	c.closers = nil
	return closers
}

func sessionKey(user, sessionID string) string {
	return user + sessionID
}

// waitForWebSession will block until the requested web session shows up in the
// cache or a timeout occurs.
func (h *Handler) waitForWebSession(ctx context.Context, req types.GetWebSessionRequest) error {
	_, err := h.cfg.AccessPoint.GetWebSession(ctx, req)
	if err == nil {
		return nil
	}
	logger := h.log.WithField("req", req)
	if !trace.IsNotFound(err) {
		logger.WithError(err).Debug("Failed to query web session.")
	}
	// Establish a watch.
	watcher, err := h.cfg.AccessPoint.NewWatcher(ctx, services.Watch{
		Name: teleport.ComponentWebProxy,
		Kinds: []services.WatchKind{
			{
				Kind:    services.KindWebSession,
				SubKind: services.KindWebSession,
			},
		},
		MetricComponent: teleport.ComponentWebProxy,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	defer watcher.Close()
	matchEvent := func(event services.Event) (services.Resource, error) {
		if event.Type == backend.OpPut &&
			event.Resource.GetKind() == services.KindWebSession &&
			event.Resource.GetName() == req.SessionID {
			return event.Resource, nil
		}
		return nil, trace.CompareFailed("not match")
	}
	_, err = local.WaitForEvent(ctx, watcher, local.EventMatcherFunc(matchEvent), h.clock)
	if err != nil {
		logger.WithError(err).Warn("Failed to wait for web session.")
	}
	return trace.Wrap(err)
}
