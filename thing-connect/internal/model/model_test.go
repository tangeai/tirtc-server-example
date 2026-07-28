package model_test

import (
	"testing"
	"thing-connect/internal/model"
)

func TestFingerprintIsEmpty(t *testing.T) {
	if !(model.Fingerprint{}).IsEmpty() {
		t.Error("empty Fingerprint should be IsEmpty")
	}
	if (model.Fingerprint{MAC: "AA:BB"}).IsEmpty() {
		t.Error("Fingerprint with MAC should not be IsEmpty")
	}
	// chip_uid/device_rand 不再构成指纹
	if !(model.Fingerprint{ChipUID: "0xAB", DeviceRand: "r"}).IsEmpty() {
		t.Error("Fingerprint with only legacy fields should be IsEmpty (MAC is the identity)")
	}
}
