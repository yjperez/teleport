package u2f

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/flynn/u2f/u2fhid"
	"github.com/flynn/u2f/u2ftoken"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/trace"
	"github.com/tstranex/u2f"
)

// Authentication sequence:
//
//    *client*                      *messages over network*            *server*
//
//                                                                 AuthenticateInit()
//                             <------ AuthenticateChallenge -----
// AuthenticateSignChallenge()
//                             -- AuthenticateChallengeResponse ->
//                                                                 AuthenticateVerify()

type (
	// AuthenticateChallenge is the first message in authentication sequence.
	// It's sent from the server to the client.
	AuthenticateChallenge = u2f.SignRequest
	// AuthenticateChallengeResponse is the second message in authentication
	// sequence. It's sent from the client to the server in response to
	// AuthenticateChallenge.
	AuthenticateChallengeResponse = u2f.SignResponse
)

// AuthenticationStorage is the persistent storage needed to store state
// (challenges and counters) during the authentication sequence.
type AuthenticationStorage interface {
	GetU2FRegistration(key string) (*Registration, error)

	UpsertU2FSignChallenge(key string, u2fChallenge *Challenge) error
	GetU2FSignChallenge(key string) (*Challenge, error)

	UpsertU2FRegistrationCounter(key string, counter uint32) error
	GetU2FRegistrationCounter(key string) (uint32, error)
}

// AuthenticateInitParams are the parameters for initiating the authentication
// sequence.
type AuthenticateInitParams struct {
	AppConfig  types.U2F
	StorageKey string
	Storage    AuthenticationStorage
}

// AuthenticateInit is the first step in the authentication sequence. It runs
// on the server and the returned AuthenticateChallenge must be sent to the
// client.
func AuthenticateInit(params AuthenticateInitParams) (*AuthenticateChallenge, error) {
	reg, err := params.Storage.GetU2FRegistration(params.StorageKey)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	challenge, err := NewChallenge(params.AppConfig.AppID, params.AppConfig.Facets)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if err = params.Storage.UpsertU2FSignChallenge(params.StorageKey, challenge); err != nil {
		return nil, trace.Wrap(err)
	}

	return challenge.SignRequest(*reg), nil
}

// AuthenticateSignChallenge is the second step in the authentication sequence.
// It runs on the client and the returned AuthenticationChallengeResponse must
// be sent to the server.
//
// Note: this function writes user interaction prompts to stdout.
func AuthenticateSignChallenge(c AuthenticateChallenge, facet string) (*AuthenticateChallengeResponse, error) {
	// Open a U2F device.
	devices, err := u2fhid.Devices()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if len(devices) == 0 {
		return nil, trace.NotFound("no u2f devices found, please plug one in to authenticate")
	}
	// TODO(awly): support multiple plugged in devices, not just the first one.
	dev, err := u2fhid.Open(devices[0])
	if err != nil {
		return nil, trace.Wrap(err)
	}
	defer dev.Close()
	tok := u2ftoken.NewToken(dev)

	// Because of the differences between github.com/tstranex/u2f and
	// github.com/flynn/u2f, we need to do some data massaging.
	//
	// tstranex/u2f data formats are, frankly, weird. Some fields are
	// base64-encoded (without padding) and some are not. And server-side
	// validation breaks if you don't follow their exact formats. I suspect
	// it's tied to some web browser behaviors, so we'll just go ahead and
	// stick with this.
	base64Encoding := base64.RawURLEncoding.WithPadding(base64.NoPadding)
	keyHandle, err := base64Encoding.DecodeString(c.KeyHandle)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	application := sha256.Sum256([]byte(c.AppID))

	// Challenge field in u2f.SignRequest is not the challenge you're supposed
	// to send to the u2f token. Instead, you put the Challenge as a field of a
	// JSON object (ClientData, with some other metadata) and hash that.
	//
	// Read more at
	// https://fidoalliance.org/specs/fido-u2f-v1.2-ps-20170411/fido-u2f-raw-message-formats-v1.2-ps-20170411.html#authentication-messages
	cd := u2f.ClientData{
		Typ:       "navigator.id.getAssertion",
		Challenge: c.Challenge,
		Origin:    facet,
	}
	cdJSON, err := json.Marshal(cd)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	cdHash := sha256.Sum256(cdJSON)

	req := u2ftoken.AuthenticateRequest{
		Challenge:   cdHash[:],
		Application: application[:],
		KeyHandle:   keyHandle,
	}
	if err := tok.CheckAuthenticate(req); err != nil {
		return nil, trace.Wrap(err)
	}

	fmt.Println("Please press the button on your U2F key")
	var res *u2ftoken.AuthenticateResponse
	start := time.Now()
	for {
		if time.Since(start) > time.Minute {
			return nil, trace.LimitExceeded("timed out waiting for a U2F key press")
		}
		res, err = tok.Authenticate(req)
		if err == u2ftoken.ErrPresenceRequired {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err != nil {
			return nil, trace.Wrap(err)
		}
		break
	}

	return &AuthenticateChallengeResponse{
		KeyHandle:     c.KeyHandle,
		SignatureData: base64Encoding.EncodeToString(res.RawResponse),
		ClientData:    base64Encoding.EncodeToString(cdJSON),
	}, nil
}

// AuthenticateVerifyParams are the parameters for verifying the
// AuthenticationChallengeResponse.
type AuthenticateVerifyParams struct {
	Resp       AuthenticateChallengeResponse
	StorageKey string
	Storage    AuthenticationStorage
}

// AuthenticateVerify is the last step in the authentication sequence. It runs
// on the server and verifies the AuthenticateChallengeResponse returned by the
// client.
func AuthenticateVerify(params AuthenticateVerifyParams) error {
	reg, err := params.Storage.GetU2FRegistration(params.StorageKey)
	if err != nil {
		return trace.Wrap(err)
	}
	counter, err := params.Storage.GetU2FRegistrationCounter(params.StorageKey)
	if err != nil {
		return trace.Wrap(err)
	}
	challenge, err := params.Storage.GetU2FSignChallenge(params.StorageKey)
	if err != nil {
		return trace.Wrap(err)
	}
	newCounter, err := reg.Authenticate(params.Resp, *challenge, counter)
	if err != nil {
		return trace.Wrap(err)
	}
	if err := params.Storage.UpsertU2FRegistrationCounter(params.StorageKey, newCounter); err != nil {
		return trace.Wrap(err)
	}
	return nil
}
