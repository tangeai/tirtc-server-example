package mqttc

import (
	"context"
	"testing"
)

func TestUninitializedBrokerIsNotReady(t *testing.T) {
	if (&Broker{}).Ping(context.Background()) == nil {
		t.Fatal("uninitialized broker reported ready")
	}
}
