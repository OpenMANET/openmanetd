package handlers

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/godbus/dbus/v5"
	bluetoothv1 "github.com/openmanet/openmanetd/internal/api/openmanet/bluetooth/v1"
	"github.com/openmanet/openmanetd/internal/bluetooth"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/emptypb"
)

type BluetoothManager interface {
	Status(context.Context) (bluetooth.Status, error)
	Begin(context.Context, dbus.ObjectPath) error
	Discover(context.Context, time.Duration) error
	Connect(context.Context, dbus.ObjectPath) error
	Activate(context.Context) error
	Disconnect(context.Context) error
	Off(context.Context) error
	Forget(context.Context) error
	TestTone(context.Context) error
	SetAutoEnable(context.Context, bool) error
}

type BluetoothService struct {
	Log     zerolog.Logger
	Manager BluetoothManager
}

func (s *BluetoothService) GetBluetoothStatus(ctx context.Context, _ *emptypb.Empty) (*bluetoothv1.GetBluetoothStatusResponse, error) {
	if s.Manager == nil {
		return &bluetoothv1.GetBluetoothStatusResponse{}, nil
	}
	status, err := s.Manager.Status(ctx)
	if err != nil {
		return nil, s.rpcError(err)
	}
	state := status.State
	response := &bluetoothv1.GetBluetoothStatusResponse{
		Available: len(status.Inventory.Controllers) > 0, Enabled: state.Enabled, ConnectionAllowed: state.ConnectAllowed,
		Active: state.Active, Ready: state.Ready, AudioReady: state.AudioReady, ButtonsReady: state.ButtonsReady, HfpReady: state.HFPReady,
		ButtonPressed: state.ButtonPressed, AutoEnable: status.Settings.AutoEnable, SelectedDevicePath: string(status.Selected.Path),
		SavedDeviceAddress: status.Settings.DeviceAddress, SavedDeviceName: status.Settings.DeviceName,
	}
	for _, controller := range status.Inventory.Controllers {
		response.Controllers = append(response.Controllers, &bluetoothv1.BluetoothController{Path: string(controller.Path), Address: controller.Address, Name: controller.Name, Powered: controller.Powered, Discovering: controller.Discovering})
	}
	for _, accessory := range status.Inventory.Accessories {
		response.Accessories = append(response.Accessories, &bluetoothv1.BluetoothAccessory{Path: string(accessory.Path), ControllerPath: string(accessory.Adapter), Address: accessory.Address, Name: accessory.Name, Paired: accessory.Paired, Connected: accessory.Connected, Supported: accessory.Supported})
	}
	return response, nil
}

func (s *BluetoothService) ExecuteBluetoothAction(ctx context.Context, req *bluetoothv1.ExecuteBluetoothActionRequest) (*bluetoothv1.ExecuteBluetoothActionResponse, error) {
	if s.Manager == nil {
		return nil, s.rpcError(bluetooth.ErrUnavailable)
	}
	var err error
	switch req.Action {
	case bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_BEGIN_SETUP:
		err = s.Manager.Begin(ctx, dbus.ObjectPath(req.ControllerPath))
	case bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_DISCOVER:
		err = s.Manager.Discover(ctx, 20*time.Second)
	case bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_CONNECT:
		err = s.Manager.Connect(ctx, dbus.ObjectPath(req.DevicePath))
	case bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_ACTIVATE:
		err = s.Manager.Activate(ctx)
	case bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_DISCONNECT:
		err = s.Manager.Disconnect(ctx)
	case bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_OFF:
		err = s.Manager.Off(ctx)
	case bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_FORGET:
		err = s.Manager.Forget(ctx)
	case bluetoothv1.BluetoothAction_BLUETOOTH_ACTION_TEST_TONE:
		err = s.Manager.TestTone(ctx)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown Bluetooth action"))
	}
	if err != nil {
		return nil, s.rpcError(err)
	}
	return &bluetoothv1.ExecuteBluetoothActionResponse{}, nil
}

func (s *BluetoothService) UpdateBluetoothPreferences(ctx context.Context, req *bluetoothv1.UpdateBluetoothPreferencesRequest) (*bluetoothv1.UpdateBluetoothPreferencesResponse, error) {
	if s.Manager == nil {
		return nil, s.rpcError(bluetooth.ErrUnavailable)
	}
	if err := s.Manager.SetAutoEnable(ctx, req.AutoEnable); err != nil {
		return nil, s.rpcError(err)
	}
	return &bluetoothv1.UpdateBluetoothPreferencesResponse{}, nil
}

func (s *BluetoothService) rpcError(err error) error {
	code := connect.CodeInternal
	switch {
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	case errors.Is(err, bluetooth.ErrUnavailable), errors.Is(err, bluetooth.ErrDisabled), errors.Is(err, bluetooth.ErrNotReady):
		code = connect.CodeFailedPrecondition
	}
	if code == connect.CodeInternal {
		s.Log.Error().Err(err).Msg("Bluetooth operation failed")
	} else {
		s.Log.Debug().Err(err).Msg("Bluetooth operation unavailable")
	}
	return connect.NewError(code, err)
}
