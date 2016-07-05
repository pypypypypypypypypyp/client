// Copyright 2015 Keybase, Inc. All rights reserved. Use of
// this source code is governed by the included BSD license.

package engine

import (
	"testing"

	"github.com/keybase/client/go/libkb"
	keybase1 "github.com/keybase/client/go/protocol"
)

func getActiveDevicesAndKeys(tc libkb.TestContext, u *FakeUser) ([]*libkb.Device, []libkb.GenericKey) {
	arg := libkb.NewLoadUserByNameArg(tc.G, u.Username)
	arg.PublicKeyOptional = true
	user, err := libkb.LoadUser(arg)
	if err != nil {
		tc.T.Fatal(err)
	}
	sibkeys := user.GetComputedKeyFamily().GetAllActiveSibkeys()
	subkeys := user.GetComputedKeyFamily().GetAllActiveSubkeys()

	activeDevices := []*libkb.Device{}
	for _, device := range user.GetComputedKeyFamily().GetAllDevices() {
		if device.Status != nil && *device.Status == libkb.DeviceStatusActive {
			activeDevices = append(activeDevices, device)
		}
	}
	return activeDevices, append(sibkeys, subkeys...)
}

func doRevokeKey(tc libkb.TestContext, u *FakeUser, kid keybase1.KID) error {
	revokeEngine := NewRevokeKeyEngine(kid, tc.G)
	ctx := &Context{
		LogUI:    tc.G.UI.GetLogUI(),
		SecretUI: u.NewSecretUI(),
	}
	err := RunEngine(revokeEngine, ctx)
	return err
}

func doRevokeDevice(tc libkb.TestContext, u *FakeUser, id keybase1.DeviceID, force bool) error {
	revokeEngine := NewRevokeDeviceEngine(RevokeDeviceEngineArgs{ID: id, Force: force}, tc.G)
	ctx := &Context{
		LogUI:    tc.G.UI.GetLogUI(),
		SecretUI: u.NewSecretUI(),
	}
	err := RunEngine(revokeEngine, ctx)
	return err
}

func assertNumDevicesAndKeys(tc libkb.TestContext, u *FakeUser, numDevices, numKeys int) {
	devices, keys := getActiveDevicesAndKeys(tc, u)
	if len(devices) != numDevices {
		for i, d := range devices {
			tc.T.Logf("device %d: %+v", i, d)
		}
		tc.T.Fatalf("Expected to find %d devices. Found %d.", numDevices, len(devices))
	}
	if len(keys) != numKeys {
		for i, k := range keys {
			tc.T.Logf("key %d: %+v", i, k)
		}
		tc.T.Fatalf("Expected to find %d keys. Found %d.", numKeys, len(keys))
	}
}

func TestRevokeDevice(t *testing.T) {
	tc := SetupEngineTest(t, "rev")
	defer tc.Cleanup()

	u := CreateAndSignupFakeUserPaper(tc, "rev")

	assertNumDevicesAndKeys(tc, u, 2, 4)

	devices, _ := getActiveDevicesAndKeys(tc, u)
	var thisDevice *libkb.Device
	for _, device := range devices {
		if device.Type != libkb.DeviceTypePaper {
			thisDevice = device
		}
	}

	// Revoking the current device should fail.
	err := doRevokeDevice(tc, u, thisDevice.ID, false)
	if err == nil {
		tc.T.Fatal("Expected revoking the current device to fail.")
	}

	assertNumDevicesAndKeys(tc, u, 2, 4)

	// But it should succeed with the --force flag.
	err = doRevokeDevice(tc, u, thisDevice.ID, true)
	if err != nil {
		tc.T.Fatal(err)
	}

	assertNumDevicesAndKeys(tc, u, 1, 2)
}

func TestRevokeKey(t *testing.T) {
	tc := SetupEngineTest(t, "rev")
	defer tc.Cleanup()

	u := createFakeUserWithPGPSibkeyPaper(tc)

	assertNumDevicesAndKeys(tc, u, 2, 5)

	_, keys := getActiveDevicesAndKeys(tc, u)
	var pgpKey *libkb.GenericKey
	for i, key := range keys {
		if libkb.IsPGP(key) {
			// XXX: Don't use &key. That refers to the loop variable, which
			// gets overwritten.
			pgpKey = &keys[i] //
			break
		}
	}
	if pgpKey == nil {
		t.Fatal("Expected to find PGP key")
	}

	err := doRevokeKey(tc, u, (*pgpKey).GetKID())
	if err != nil {
		tc.T.Fatal(err)
	}

	assertNumDevicesAndKeys(tc, u, 2, 4)
}

// See issue #370.
func TestTrackAfterRevoke(t *testing.T) {
	tc1 := SetupEngineTest(t, "rev")
	defer tc1.Cleanup()

	// We need two devices.  Going to use GPG to sign second device.

	// Sign up on tc1:
	u := CreateAndSignupFakeUserGPG(tc1, "pgp")

	// Redo SetupEngineTest to get a new home directory...should look like a new device.
	tc2 := SetupEngineTest(t, "login")
	defer tc2.Cleanup()

	// We need the gpg keyring that's in the first device homedir:
	if err := tc1.MoveGpgKeyringTo(tc2); err != nil {
		t.Fatal(err)
	}

	// Login on device tc2.  It will use gpg to sign the device.
	ctx := &Context{
		ProvisionUI: newTestProvisionUIGPGImport(),
		LogUI:       tc2.G.UI.GetLogUI(),
		SecretUI:    u.NewSecretUI(),
		LoginUI:     &libkb.TestLoginUI{Username: u.Username},
		GPGUI:       &gpgtestui{},
	}
	li := NewLogin(tc2.G, libkb.DeviceTypeDesktop, "", keybase1.ClientType_CLI)
	if err := RunEngine(li, ctx); err != nil {
		t.Fatal(err)
	}

	// tc2 revokes tc1 device:
	err := doRevokeDevice(tc2, u, tc1.G.Env.GetDeviceID(), false)
	if err != nil {
		tc2.T.Fatal(err)
	}

	// Still logged in on tc1.  Try to use it to track someone.  It should fail
	// with a KeyRevokedError.
	_, _, err = runTrack(tc1, u, "t_alice")
	if err == nil {
		t.Fatal("expected runTrack to return an error")
	}
	if _, ok := err.(libkb.KeyRevokedError); !ok {
		t.Errorf("expected libkb.KeyRevokedError, got %T", err)
	}
}

func TestSignAfterRevoke(t *testing.T) {
	tc1 := SetupEngineTest(t, "rev")
	defer tc1.Cleanup()

	// We need two devices.  Going to use GPG to sign second device.

	// Sign up on tc1:
	u := CreateAndSignupFakeUserGPG(tc1, "pgp")

	// Redo SetupEngineTest to get a new home directory...should look like a new device.
	tc2 := SetupEngineTest(t, "login")
	defer tc2.Cleanup()

	// We need the gpg keyring that's in the first device homedir:
	if err := tc1.MoveGpgKeyringTo(tc2); err != nil {
		t.Fatal(err)
	}

	// Login on device tc2.  It will use gpg to sign the device.
	ctx := &Context{
		ProvisionUI: newTestProvisionUIGPGImport(),
		LogUI:       tc2.G.UI.GetLogUI(),
		SecretUI:    u.NewSecretUI(),
		LoginUI:     &libkb.TestLoginUI{Username: u.Username},
		GPGUI:       &gpgtestui{},
	}
	li := NewLogin(tc2.G, libkb.DeviceTypeDesktop, "", keybase1.ClientType_CLI)
	if err := RunEngine(li, ctx); err != nil {
		t.Fatal(err)
	}

	// tc2 revokes tc1 device:
	err := doRevokeDevice(tc2, u, tc1.G.Env.GetDeviceID(), false)
	if err != nil {
		tc2.T.Fatal(err)
	}

	// Still logged in on tc1, a revoked device.

	// Test signing with (revoked) device key on tc1, which works...
	msg := []byte("test message")
	ret, err := SignED25519(tc1.G, u.NewSecretUI(), keybase1.SignED25519Arg{
		Msg: msg,
	})
	if err != nil {
		t.Fatal(err)
	}
	publicKey := libkb.NaclSigningKeyPublic(ret.PublicKey)
	if !publicKey.Verify(msg, (*libkb.NaclSignature)(&ret.Sig)) {
		t.Error(libkb.VerificationError{})
	}

	// This should log out tc1:
	if err := tc1.G.LogoutIfRevoked(); err != nil {
		t.Fatal(err)
	}

	AssertLoggedOut(tc1)

	// And now this should fail.
	ret, err = SignED25519(tc1.G, u.NewSecretUI(), keybase1.SignED25519Arg{
		Msg: msg,
	})
	if err == nil {
		t.Fatal("nil error signing after LogoutIfRevoked")
	}
	if _, ok := err.(libkb.KeyRevokedError); !ok {
		t.Errorf("error type: %T, expected libkb.KeyRevokedError", err)
	}
}

// Check that if not on a revoked device that LogoutIfRevoked doesn't do anything.
func TestLogoutIfRevokedNoop(t *testing.T) {
	tc := SetupEngineTest(t, "rev")
	defer tc.Cleanup()

	u := CreateAndSignupFakeUser(tc, "rev")

	AssertLoggedIn(tc)

	if err := tc.G.LogoutIfRevoked(); err != nil {
		t.Fatal(err)
	}

	AssertLoggedIn(tc)

	msg := []byte("test message")
	ret, err := SignED25519(tc.G, u.NewSecretUI(), keybase1.SignED25519Arg{
		Msg: msg,
	})
	if err != nil {
		t.Fatal(err)
	}
	publicKey := libkb.NaclSigningKeyPublic(ret.PublicKey)
	if !publicKey.Verify(msg, (*libkb.NaclSignature)(&ret.Sig)) {
		t.Error(libkb.VerificationError{})
	}
}
