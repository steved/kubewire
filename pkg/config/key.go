package config

import (
	"encoding"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Key wraps wgtypes.Key to allow readable marshal to YAML
type Key struct {
	wgtypes.Key
}

var _ encoding.TextMarshaler = Key{}

// MarshalText implements the TextMarshaler interface
func (k Key) MarshalText() ([]byte, error) {
	return []byte(k.String()), nil
}

// UnmarshalText implements the TextMarshaler interface
func (k *Key) UnmarshalText(text []byte) (err error) {
	k.Key, err = wgtypes.ParseKey(string(text))
	return
}
