package handlers_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	bluetoothv1 "github.com/openmanet/openmanetd/internal/api/openmanet/bluetooth/v1"
	"github.com/openmanet/openmanetd/internal/bluetooth"
	"github.com/openmanet/openmanetd/internal/openmanet/server/handlers"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestBluetoothUnavailableStatusIsPassive(t *testing.T) {
	svc := &handlers.BluetoothService{Log: zerolog.Nop()}
	status, err := svc.GetBluetoothStatus(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	require.False(t, status.Available)
	require.False(t, status.AutoEnable)
	_, err = svc.ExecuteBluetoothAction(context.Background(), &bluetoothv1.ExecuteBluetoothActionRequest{Action: bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_BEGIN_SETUP, ControllerPath: "/org/bluez/hci0"})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

func TestBluetoothActionsAndPreferences(t *testing.T) {
	manager := &fakeBluetoothManager{}
	svc := &handlers.BluetoothService{Log: zerolog.Nop(), Manager: manager}
	for _, tc := range []struct {
		action bluetoothv1.BluetoothAction
		call   string
	}{
		{bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_BEGIN_SETUP, "begin:/org/bluez/hci0"},
		{bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_DISCOVER, "discover:20s"},
		{bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_CONNECT, "connect:/org/bluez/hci0/dev_AA"},
		{bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_ACTIVATE, "activate"},
		{bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_DISCONNECT, "disconnect"},
		{bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_OFF, "off"},
		{bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_FORGET, "forget"},
		{bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_TEST_TONE, "tone"},
	} {
		_, err := svc.ExecuteBluetoothAction(context.Background(), &bluetoothv1.ExecuteBluetoothActionRequest{Action: tc.action, ControllerPath: "/org/bluez/hci0", DevicePath: "/org/bluez/hci0/dev_AA"})
		require.NoError(t, err)
		calls := manager.BluetoothCalls()
		require.Equal(t, tc.call, calls[len(calls)-1])
	}
	_, err := svc.UpdateBluetoothPreferences(context.Background(), &bluetoothv1.UpdateBluetoothPreferencesRequest{AutoEnable: true})
	require.NoError(t, err)
	require.Contains(t, manager.BluetoothCalls(), "auto:true")
}

func TestBluetoothReadinessFailureIsPrecondition(t *testing.T) {
	svc := &handlers.BluetoothService{Log: zerolog.Nop(), Manager: &fakeBluetoothManager{err: bluetooth.ErrNotReady}}
	_, err := svc.ExecuteBluetoothAction(context.Background(), &bluetoothv1.ExecuteBluetoothActionRequest{Action: bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_ACTIVATE})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

func TestBluetoothActionValidation(t *testing.T) {
	validator := newValidator(t)
	for _, req := range []*bluetoothv1.ExecuteBluetoothActionRequest{
		{}, {Action: 99}, {Action: bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_BEGIN_SETUP}, {Action: bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_CONNECT},
	} {
		require.Error(t, validator.Validate(req))
	}
	require.NoError(t, validator.Validate(&bluetoothv1.ExecuteBluetoothActionRequest{Action: bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_OFF}))
	require.NoError(t, validator.Validate(&bluetoothv1.ExecuteBluetoothActionRequest{Action: bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_CONNECT, DevicePath: "/org/bluez/hci0/dev_AA"}))
}
