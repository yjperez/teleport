/*
Copyright 2021 Gravitational, Inc.

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

package u2f

import (
	"fmt"
	"time"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/trace"
	"github.com/marshallbrekka/go-u2fhost"
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
	// Convert request struct between the different libraries.
	req := &u2fhost.AuthenticateRequest{
		Challenge: c.Challenge,
		AppId:     c.AppID,
		Facet:     facet,
		KeyHandle: c.KeyHandle,
		CheckOnly: true,
	}

	// Open available U2F devices.
	allDevices := u2fhost.Devices()
	if len(allDevices) == 0 {
		return nil, trace.NotFound("no u2f devices found, please plug one in to authenticate")
	}
	// Filter only the devices that can be opened and are registered for this user.
	var openDevices []u2fhost.Device
	for _, device := range allDevices {
		if err := device.Open(); err != nil {
			continue
		}
		// Call Authenticate with CheckOnly set in AuthenticateRequest, which
		// will check whether this device has the provided KeyHandle.
		if _, err := device.Authenticate(req); err != nil {
			// BadKeyHandleError is expected when this device doesn't have the
			// KeyHandle.
			if _, ok := err.(*u2fhost.BadKeyHandleError); ok {
				continue
			}
			// TestOfUserPresenceRequiredError is expected when this device
			// *does* have the KeyHandle. Any other kind of error is
			// unexpected.
			if _, ok := err.(*u2fhost.TestOfUserPresenceRequiredError); !ok {
				continue
			}
		}

		openDevices = append(openDevices, device)
		defer func(device u2fhost.Device) {
			device.Close()
		}(device)
	}
	if len(openDevices) == 0 {
		return nil, trace.NotFound("found %d u2f devices, but none of them are registered with this Teleport user", len(allDevices))
	}

	fmt.Println("Please press the button on your U2F key")
	// Unset CheckOnly to perform the actual challenge signing.
	req.CheckOnly = false

	var res *u2fhost.AuthenticateResponse
	var err error
	start := time.Now()
outer:
	for {
		if time.Since(start) > time.Minute {
			return nil, trace.LimitExceeded("timed out waiting for a U2F key press")
		}
		// The below device.Authenticate calls are non-blocking. They return
		// u2fhost.TestOfUserPresenceRequiredError until the user actually
		// touches the device button.
		for _, device := range openDevices {
			res, err = device.Authenticate(req)
			if err != nil {
				if _, ok := err.(*u2fhost.TestOfUserPresenceRequiredError); ok {
					continue
				}
				return nil, trace.Wrap(err)
			}
			break outer
		}
		time.Sleep(200 * time.Millisecond)
	}

	return &AuthenticateChallengeResponse{
		KeyHandle:     res.KeyHandle,
		SignatureData: res.SignatureData,
		ClientData:    res.ClientData,
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
