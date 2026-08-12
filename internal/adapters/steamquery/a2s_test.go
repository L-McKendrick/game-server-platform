package steamquery

import (
	"context"
	"encoding/binary"
	"math"
	"net"
	"testing"
	"time"
)

func TestClientQuery_ReturnsLiveCountAndPlayerNames(t *testing.T) {
	t.Parallel()
	server := startTestServer(t, true, true)
	client, err := New(server.port, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	status, err := client.Query(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if status.PlayerCount != 2 || status.MaxPlayers != 32 {
		t.Fatalf("player status = %#v; want 2/32", status)
	}
	if len(status.PlayerNames) != 2 || status.PlayerNames[0] != "Alice" || status.PlayerNames[1] != "Bob" {
		t.Fatalf("player names = %#v; want Alice and Bob", status.PlayerNames)
	}
}

func TestClientQuery_PlayerDetailFailureDoesNotTurnCountIntoZero(t *testing.T) {
	t.Parallel()
	server := startTestServer(t, false, false)
	client, err := New(server.port, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	status, err := client.Query(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if status.PlayerCount != 2 || status.MaxPlayers != 32 || len(status.PlayerNames) != 0 {
		t.Fatalf("player status = %#v; want count without names", status)
	}
}

func TestNewRejectsInvalidBounds(t *testing.T) {
	t.Parallel()
	if _, err := New(70000, time.Second); err == nil {
		t.Fatal("New() succeeded for invalid port")
	}
	if _, err := New(2303, 50*time.Millisecond); err == nil {
		t.Fatal("New() succeeded for invalid timeout")
	}
}

type testServer struct{ port int }

func startTestServer(t *testing.T, playerDetails bool, infoChallenge bool) testServer {
	t.Helper()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			request := make([]byte, 1024)
			count, remote, err := listener.ReadFromUDP(request)
			if err != nil {
				return
			}
			request = request[:count]
			if len(request) < 5 {
				continue
			}
			switch request[4] {
			case infoRequestType:
				if infoChallenge && len(request) == 25 {
					_, _ = listener.WriteToUDP([]byte{0xFF, 0xFF, 0xFF, 0xFF, challengeType, 0x04, 0x03, 0x02, 0x01}, remote)
					continue
				}
				_, _ = listener.WriteToUDP(infoResponse(), remote)
			case playerRequestType:
				if !playerDetails {
					_, _ = listener.WriteToUDP([]byte{0xFF, 0xFF, 0xFF, 0xFF, 'E'}, remote)
					continue
				}
				if len(request) < 9 || binary.LittleEndian.Uint32(request[5:9]) == math.MaxUint32 {
					_, _ = listener.WriteToUDP([]byte{0xFF, 0xFF, 0xFF, 0xFF, challengeType, 0x04, 0x03, 0x02, 0x01}, remote)
					continue
				}
				_, _ = listener.WriteToUDP(playerResponse(), remote)
			}
		}
	}()
	return testServer{port: listener.LocalAddr().(*net.UDPAddr).Port}
}

func infoResponse() []byte {
	response := []byte{0xFF, 0xFF, 0xFF, 0xFF, infoResponseType, 17}
	response = append(response, []byte("Test Server\x00Stratis\x00arma3\x00Arma 3\x00")...)
	response = binary.LittleEndian.AppendUint16(response, 123)
	return append(response, 2, 32)
}

func playerResponse() []byte {
	response := []byte{0xFF, 0xFF, 0xFF, 0xFF, playerResponseType, 2}
	for index, name := range []string{"Alice", "Bob"} {
		response = append(response, byte(index))
		response = append(response, []byte(name)...)
		response = append(response, 0)
		response = binary.LittleEndian.AppendUint32(response, 0)
		response = binary.LittleEndian.AppendUint32(response, 0)
	}
	return response
}
