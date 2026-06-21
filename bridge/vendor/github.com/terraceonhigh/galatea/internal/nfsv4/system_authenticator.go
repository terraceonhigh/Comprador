package nfsv4

import (
	"bytes"
	"context"
	"crypto/sha256"
	"sync"

	"github.com/terraceonhigh/galatea/internal/xdr/pkg/protocols/rpcv2"
	"github.com/terraceonhigh/galatea/internal/xdr/pkg/rpcserver"
)

// NOTE (Galatea Phase 1 lift, R2d): this file replaces bb-remote-execution's
// original system_authenticator.go. The upstream version mapped AUTH_SYS
// credentials onto a configurable bb-storage/pkg/auth.AuthenticationMetadata
// via a JMESPath expression, so a build cluster could drive authorization
// policy from NFS uid/gid. That apparatus was the sole importer of
// bb-storage/pkg/{auth,jmespath} (and, through them, the OpenTelemetry / GCP /
// Prometheus dependency tree) in the NFSv4 server — see DEC-011.
//
// Galatea is a single-user, localhost Finder mount with no authorization
// policy engine, so we keep only what AUTH_SYS genuinely requires: parse the
// credential body and attach the caller's uid/gid to the context for any host
// backend that cares to read it. The COMPOUND/state-machine logic never reads
// it. This drops auth, jmespath, and eviction from the build.

// Credentials are the identity carried by an AUTH_SYS RPC credential (RFC 5531,
// appendix A). A host backend may read these from the request context to make
// its own access decisions; Galatea itself does not.
type Credentials struct {
	Stamp       uint32
	MachineName string
	UID         uint32
	GID         uint32
	GIDs        []uint32
}

type credentialsContextKey struct{}

// WithCredentials attaches AUTH_SYS credentials to a context.
func WithCredentials(ctx context.Context, c *Credentials) context.Context {
	return context.WithValue(ctx, credentialsContextKey{}, c)
}

// CredentialsFromContext returns the AUTH_SYS credentials attached to a
// context, if any.
func CredentialsFromContext(ctx context.Context) (*Credentials, bool) {
	c, ok := ctx.Value(credentialsContextKey{}).(*Credentials)
	return c, ok
}

// SystemAuthenticatorCacheKey keys the AUTH_SHORT replay cache.
type SystemAuthenticatorCacheKey [sha256.Size]byte

type systemAuthenticator struct {
	lock  sync.Mutex
	cache map[SystemAuthenticatorCacheKey]*Credentials
}

// NewSystemAuthenticator is an RPCv2 Authenticator that accepts AUTH_SYS
// credentials (RFC 5531, appendix A), parses them, and attaches the resulting
// Credentials to the request context. It also honours the AUTH_SHORT shorthand
// the client may use on subsequent calls. Suitable for a single-user localhost
// mount; it imposes no authorization policy.
func NewSystemAuthenticator() rpcserver.Authenticator {
	return &systemAuthenticator{
		cache: map[SystemAuthenticatorCacheKey]*Credentials{},
	}
}

func (a *systemAuthenticator) Authenticate(ctx context.Context, credentials, verifier *rpcv2.OpaqueAuth) (context.Context, rpcv2.OpaqueAuth, rpcv2.AuthStat) {
	switch credentials.Flavor {
	case rpcv2.AUTH_SYS:
		key := sha256.Sum256(credentials.Body)

		a.lock.Lock()
		defer a.lock.Unlock()

		creds, ok := a.cache[key]
		if !ok {
			// Parse system authentication data.
			var body rpcv2.AuthsysParms
			b := bytes.NewBuffer(credentials.Body)
			if _, err := body.ReadFrom(b); err != nil || b.Len() != 0 {
				return nil, rpcv2.OpaqueAuth{}, rpcv2.AUTH_BADCRED
			}
			gids := make([]uint32, len(body.Gids))
			copy(gids, body.Gids)
			creds = &Credentials{
				Stamp:       body.Stamp,
				MachineName: body.Machinename,
				UID:         body.Uid,
				GID:         body.Gid,
				GIDs:        gids,
			}
			a.cache[key] = creds
		}
		return WithCredentials(ctx, creds),
			rpcv2.OpaqueAuth{
				Flavor: rpcv2.AUTH_SHORT,
				Body:   key[:],
			},
			rpcv2.AUTH_OK
	case rpcv2.AUTH_SHORT:
		if len(credentials.Body) != sha256.Size {
			return nil, rpcv2.OpaqueAuth{}, rpcv2.AUTH_BADCRED
		}
		var key SystemAuthenticatorCacheKey
		copy(key[:], credentials.Body)

		a.lock.Lock()
		defer a.lock.Unlock()

		if creds, ok := a.cache[key]; ok {
			return WithCredentials(ctx, creds),
				rpcv2.OpaqueAuth{Flavor: rpcv2.AUTH_NONE},
				rpcv2.AUTH_OK
		}
		// The client must provide the original credentials again.
		return nil, rpcv2.OpaqueAuth{}, rpcv2.AUTH_REJECTEDCRED
	default:
		return nil, rpcv2.OpaqueAuth{}, rpcv2.AUTH_BADCRED
	}
}
