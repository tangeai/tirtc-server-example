package installer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type mqttConnectPacket struct {
	clientID string
	username string
	password string
}

func TestProbeMQTTExternalUsernameAuthentication(t *testing.T) {
	broker := strings.TrimSpace(os.Getenv("THING_CONNECT_TEST_MQTT_BROKER"))
	username := os.Getenv("THING_CONNECT_TEST_MQTT_USERNAME")
	password := os.Getenv("THING_CONNECT_TEST_MQTT_PASSWORD")
	if broker == "" || username == "" {
		t.Skip("external MQTT test credentials are not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	input := MQTTInput{
		Broker: broker, AuthMode: mqttAuthUsername,
		Username: username, Password: password,
	}
	if err := probeMQTT(ctx, input, nil); err != nil {
		t.Fatalf("external MQTT username probe failed: %v", err)
	}
}

func TestProbeMQTTUsernameAuthenticationOnWire(t *testing.T) {
	broker, packets := startMQTTTestBroker(t, 1)
	input := MQTTInput{
		Broker: broker, AuthMode: mqttAuthUsername,
		Username: "shared-services", Password: "mqtt-password",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := probeMQTT(ctx, input, nil); err != nil {
		t.Fatal(err)
	}
	packet := receiveMQTTConnect(t, packets)
	if !strings.HasPrefix(packet.clientID, "thingconnect-setup-") {
		t.Fatalf("client ID = %q", packet.clientID)
	}
	if packet.username != input.Username || packet.password != input.Password {
		t.Fatalf("credentials = username %q password length %d", packet.username, len(packet.password))
	}
}

func TestProbeMQTTClientIDAuthenticationOnWire(t *testing.T) {
	want := map[string]bool{
		"devicesrv": false,
		"usrsrv":    false,
		"voipsrv":   false,
		"callsrv":   false,
	}
	broker, packets := startMQTTTestBroker(t, len(want))
	input := MQTTInput{
		Broker: broker, AuthMode: mqttAuthClientID, Password: "mqtt-password",
		ClientIDs: map[string]string{
			"device-server": "devicesrv", "user-server": "usrsrv",
			"voip-server": "voipsrv", "call-server": "callsrv",
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := probeMQTT(ctx, input, []string{"voip-server", "call-server"}); err != nil {
		t.Fatal(err)
	}
	for range want {
		packet := receiveMQTTConnect(t, packets)
		if _, ok := want[packet.clientID]; !ok {
			t.Fatalf("unexpected client ID %q", packet.clientID)
		}
		if want[packet.clientID] {
			t.Fatalf("duplicate client ID %q", packet.clientID)
		}
		want[packet.clientID] = true
		if packet.username != packet.clientID || packet.password != input.Password {
			t.Fatalf("clientid credentials = client ID %q username %q password length %d", packet.clientID, packet.username, len(packet.password))
		}
	}
}

func startMQTTTestBroker(t *testing.T, connections int) (string, <-chan mqttConnectPacket) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	packets := make(chan mqttConnectPacket, connections)
	errors := make(chan error, 1)
	go func() {
		defer close(packets)
		for i := 0; i < connections; i++ {
			connection, err := listener.Accept()
			if err != nil {
				errors <- err
				return
			}
			packet, err := acceptMQTTConnect(connection)
			_ = connection.Close()
			if err != nil {
				errors <- err
				return
			}
			packets <- packet
		}
	}()
	t.Cleanup(func() {
		select {
		case err := <-errors:
			if err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
				t.Errorf("test MQTT broker: %v", err)
			}
		default:
		}
	})
	port := listener.Addr().(*net.TCPAddr).Port
	return "mqtt://127.0.0.1:" + strconv.Itoa(port), packets
}

func acceptMQTTConnect(connection net.Conn) (mqttConnectPacket, error) {
	if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return mqttConnectPacket{}, err
	}
	reader := bufio.NewReader(connection)
	header, err := reader.ReadByte()
	if err != nil {
		return mqttConnectPacket{}, err
	}
	if header != 0x10 {
		return mqttConnectPacket{}, fmt.Errorf("first MQTT packet type = %#x", header)
	}
	remaining, err := readMQTTRemainingLength(reader)
	if err != nil {
		return mqttConnectPacket{}, err
	}
	body := make([]byte, remaining)
	if _, err := io.ReadFull(reader, body); err != nil {
		return mqttConnectPacket{}, err
	}
	packet, err := parseMQTTConnect(body)
	if err != nil {
		return mqttConnectPacket{}, err
	}
	if _, err := connection.Write([]byte{0x20, 0x02, 0x00, 0x00}); err != nil {
		return mqttConnectPacket{}, err
	}
	// Keep the socket alive until Paho sends DISCONNECT. EOF is also acceptable
	// after a successful CONNACK because the wire fields have already been read.
	_, _ = reader.ReadByte()
	return packet, nil
}

func readMQTTRemainingLength(reader *bufio.Reader) (int, error) {
	value, multiplier := 0, 1
	for multiplier <= 128*128*128 {
		encoded, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		value += int(encoded&127) * multiplier
		if encoded&128 == 0 {
			return value, nil
		}
		multiplier *= 128
	}
	return 0, fmt.Errorf("invalid MQTT remaining length")
}

func parseMQTTConnect(body []byte) (mqttConnectPacket, error) {
	cursor := 0
	protocol, err := readMQTTString(body, &cursor)
	if err != nil {
		return mqttConnectPacket{}, err
	}
	if protocol != "MQTT" || cursor+4 > len(body) {
		return mqttConnectPacket{}, fmt.Errorf("unsupported MQTT CONNECT protocol %q", protocol)
	}
	level := body[cursor]
	cursor++
	if level != 4 {
		return mqttConnectPacket{}, fmt.Errorf("MQTT protocol level = %d", level)
	}
	flags := body[cursor]
	cursor++
	cursor += 2 // Keep Alive
	clientID, err := readMQTTString(body, &cursor)
	if err != nil {
		return mqttConnectPacket{}, err
	}
	if flags&0x04 != 0 {
		return mqttConnectPacket{}, fmt.Errorf("unexpected MQTT Will payload")
	}
	packet := mqttConnectPacket{clientID: clientID}
	if flags&0x80 != 0 {
		packet.username, err = readMQTTString(body, &cursor)
		if err != nil {
			return mqttConnectPacket{}, err
		}
	}
	if flags&0x40 != 0 {
		packet.password, err = readMQTTString(body, &cursor)
		if err != nil {
			return mqttConnectPacket{}, err
		}
	}
	if cursor != len(body) {
		return mqttConnectPacket{}, fmt.Errorf("unexpected MQTT CONNECT payload bytes: %d", len(body)-cursor)
	}
	return packet, nil
}

func readMQTTString(body []byte, cursor *int) (string, error) {
	if *cursor+2 > len(body) {
		return "", io.ErrUnexpectedEOF
	}
	length := int(body[*cursor])<<8 | int(body[*cursor+1])
	*cursor += 2
	if *cursor+length > len(body) {
		return "", io.ErrUnexpectedEOF
	}
	value := string(body[*cursor : *cursor+length])
	*cursor += length
	return value, nil
}

func receiveMQTTConnect(t *testing.T, packets <-chan mqttConnectPacket) mqttConnectPacket {
	t.Helper()
	select {
	case packet, ok := <-packets:
		if !ok {
			t.Fatal("test MQTT broker closed before CONNECT")
		}
		return packet
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for MQTT CONNECT")
		return mqttConnectPacket{}
	}
}
