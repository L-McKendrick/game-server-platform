// Package steamquery implements the bounded Steam A2S queries needed by the
// Discord status command. It does not retain query results or player names.
package steamquery

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const (
	defaultPort    = 2303
	defaultTimeout = 1500 * time.Millisecond
	maxPacketBytes = 64 * 1024
	maxSplitParts  = 16
	maxPlayers     = 128

	packetHeader       uint32 = 0xFFFFFFFF
	splitPacketHeader  uint32 = 0xFFFFFFFE
	infoRequestType    byte   = 'T'
	infoResponseType   byte   = 'I'
	playerRequestType  byte   = 'U'
	playerResponseType byte   = 'D'
	challengeType      byte   = 'A'
)

// Client performs Steam server-query protocol requests against the configured
// UDP port.
type Client struct {
	port    int
	timeout time.Duration
}

// New creates a client with conservative request bounds suitable for the
// Discord initial-response window.
func New(port int, timeout time.Duration) (*Client, error) {
	if port == 0 {
		port = defaultPort
	}
	if port < 1 || port > math.MaxUint16 {
		return nil, fmt.Errorf("Steam query port must be between 1 and 65535")
	}
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if timeout < 100*time.Millisecond || timeout > 2500*time.Millisecond {
		return nil, fmt.Errorf("Steam query timeout must be between 100ms and 2500ms")
	}
	return &Client{port: port, timeout: timeout}, nil
}

// Query retrieves INFO for the authoritative player count, then PLAYER for
// display names when the server reports one or more players. An unsupported or
// failed player-detail response leaves the count valid and returns no names.
func (client *Client) Query(ctx context.Context, host string) (domain.PlayerStatus, error) {
	if client == nil {
		return domain.PlayerStatus{}, fmt.Errorf("Steam query client is required")
	}
	host = strings.TrimSpace(host)
	if net.ParseIP(host) == nil {
		return domain.PlayerStatus{}, fmt.Errorf("Steam query host must be an IP address")
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "udp", net.JoinHostPort(host, strconv.Itoa(client.port)))
	if err != nil {
		return domain.PlayerStatus{}, fmt.Errorf("dial Steam query server: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(client.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return domain.PlayerStatus{}, fmt.Errorf("set Steam query deadline: %w", err)
	}

	info, err := client.info(connection)
	if err != nil {
		return domain.PlayerStatus{}, err
	}
	if info.PlayerCount == 0 {
		return info, nil
	}
	names, err := client.players(connection)
	if err == nil {
		info.PlayerNames = names
	}
	return info, nil
}

func (client *Client) info(connection net.Conn) (domain.PlayerStatus, error) {
	request := append([]byte{0xFF, 0xFF, 0xFF, 0xFF, infoRequestType}, []byte("Source Engine Query\x00")...)
	if err := writePacket(connection, request); err != nil {
		return domain.PlayerStatus{}, fmt.Errorf("send A2S_INFO request: %w", err)
	}
	payload, err := readPayload(connection)
	if err != nil {
		return domain.PlayerStatus{}, fmt.Errorf("read A2S_INFO response: %w", err)
	}
	if len(payload) == 5 && payload[0] == challengeType {
		request = binary.LittleEndian.AppendUint32(request, binary.LittleEndian.Uint32(payload[1:5]))
		if err := writePacket(connection, request); err != nil {
			return domain.PlayerStatus{}, fmt.Errorf("send A2S_INFO challenge response: %w", err)
		}
		payload, err = readPayload(connection)
		if err != nil {
			return domain.PlayerStatus{}, fmt.Errorf("read A2S_INFO challenged response: %w", err)
		}
	}
	if len(payload) == 0 || payload[0] != infoResponseType {
		return domain.PlayerStatus{}, fmt.Errorf("unexpected A2S_INFO response")
	}
	reader := packetReader{bytes: payload[1:]}
	if _, err := reader.byte(); err != nil { // protocol version
		return domain.PlayerStatus{}, fmt.Errorf("parse A2S_INFO protocol: %w", err)
	}
	for range 4 { // server name, map, folder, game
		if _, err := reader.string(); err != nil {
			return domain.PlayerStatus{}, fmt.Errorf("parse A2S_INFO string: %w", err)
		}
	}
	if _, err := reader.uint16(); err != nil { // app ID
		return domain.PlayerStatus{}, fmt.Errorf("parse A2S_INFO app ID: %w", err)
	}
	count, err := reader.byte()
	if err != nil {
		return domain.PlayerStatus{}, fmt.Errorf("parse A2S_INFO player count: %w", err)
	}
	maximum, err := reader.byte()
	if err != nil {
		return domain.PlayerStatus{}, fmt.Errorf("parse A2S_INFO maximum players: %w", err)
	}
	return domain.PlayerStatus{PlayerCount: int(count), MaxPlayers: int(maximum)}, nil
}

func (client *Client) players(connection net.Conn) ([]string, error) {
	request := []byte{0xFF, 0xFF, 0xFF, 0xFF, playerRequestType, 0xFF, 0xFF, 0xFF, 0xFF}
	if err := writePacket(connection, request); err != nil {
		return nil, fmt.Errorf("send A2S_PLAYER challenge: %w", err)
	}
	payload, err := readPayload(connection)
	if err != nil {
		return nil, fmt.Errorf("read A2S_PLAYER challenge: %w", err)
	}
	if len(payload) < 5 || payload[0] != challengeType {
		return nil, fmt.Errorf("unexpected A2S_PLAYER challenge")
	}
	challenge := binary.LittleEndian.Uint32(payload[1:5])
	binary.LittleEndian.PutUint32(request[5:], challenge)
	if err := writePacket(connection, request); err != nil {
		return nil, fmt.Errorf("send A2S_PLAYER request: %w", err)
	}
	payload, err = readPayload(connection)
	if err != nil {
		return nil, fmt.Errorf("read A2S_PLAYER response: %w", err)
	}
	if len(payload) < 2 || payload[0] != playerResponseType {
		return nil, fmt.Errorf("unexpected A2S_PLAYER response")
	}
	reader := packetReader{bytes: payload[1:]}
	count, err := reader.byte()
	if err != nil || int(count) > maxPlayers {
		return nil, fmt.Errorf("invalid A2S_PLAYER count")
	}
	names := make([]string, 0, count)
	for range count {
		if _, err := reader.byte(); err != nil { // player index
			return nil, fmt.Errorf("parse A2S_PLAYER index: %w", err)
		}
		name, err := reader.string()
		if err != nil {
			return nil, fmt.Errorf("parse A2S_PLAYER name: %w", err)
		}
		if _, err := reader.uint32(); err != nil { // score
			return nil, fmt.Errorf("parse A2S_PLAYER score: %w", err)
		}
		if _, err := reader.uint32(); err != nil { // duration float bits
			return nil, fmt.Errorf("parse A2S_PLAYER duration: %w", err)
		}
		names = append(names, name)
	}
	return names, nil
}

func writePacket(connection net.Conn, request []byte) error {
	count, err := connection.Write(request)
	if err != nil {
		return err
	}
	if count != len(request) {
		return fmt.Errorf("short UDP write")
	}
	return nil
}

func readPayload(connection net.Conn) ([]byte, error) {
	packet := make([]byte, maxPacketBytes)
	count, err := connection.Read(packet)
	if err != nil {
		return nil, err
	}
	return assemblePacket(connection, packet[:count])
}

func assemblePacket(connection net.Conn, first []byte) ([]byte, error) {
	if len(first) < 5 {
		return nil, fmt.Errorf("short Steam query packet")
	}
	header := binary.LittleEndian.Uint32(first[:4])
	if header == packetHeader {
		return first[4:], nil
	}
	if header != splitPacketHeader || len(first) < 11 {
		return nil, fmt.Errorf("invalid Steam query packet header")
	}
	packetID := binary.LittleEndian.Uint32(first[4:8])
	if packetID&0x80000000 != 0 {
		return nil, fmt.Errorf("compressed Steam query packets are unsupported")
	}
	sequence := first[8]
	total, index := int(sequence>>4), int(sequence&0x0F)
	if total < 1 || total > maxSplitParts || index >= total {
		return nil, fmt.Errorf("invalid Steam query split packet sequence")
	}
	parts := make([][]byte, total)
	parts[index] = append([]byte(nil), first[11:]...)
	for received := 1; received < total; {
		packet := make([]byte, maxPacketBytes)
		count, err := connection.Read(packet)
		if err != nil {
			return nil, err
		}
		packet = packet[:count]
		if len(packet) < 11 || binary.LittleEndian.Uint32(packet[:4]) != splitPacketHeader || binary.LittleEndian.Uint32(packet[4:8]) != packetID {
			continue
		}
		sequence = packet[8]
		if int(sequence>>4) != total || int(sequence&0x0F) >= total {
			continue
		}
		index = int(sequence & 0x0F)
		if parts[index] == nil {
			parts[index] = append([]byte(nil), packet[11:]...)
			received++
		}
	}
	result := make([]byte, 0, maxPacketBytes)
	for _, part := range parts {
		if len(result)+len(part) > maxPacketBytes {
			return nil, fmt.Errorf("Steam query response exceeds %d bytes", maxPacketBytes)
		}
		result = append(result, part...)
	}
	return result, nil
}

type packetReader struct {
	bytes []byte
	index int
}

func (reader *packetReader) byte() (byte, error) {
	if reader.index >= len(reader.bytes) {
		return 0, fmt.Errorf("unexpected end of packet")
	}
	value := reader.bytes[reader.index]
	reader.index++
	return value, nil
}

func (reader *packetReader) uint16() (uint16, error) {
	if len(reader.bytes)-reader.index < 2 {
		return 0, fmt.Errorf("unexpected end of packet")
	}
	value := binary.LittleEndian.Uint16(reader.bytes[reader.index:])
	reader.index += 2
	return value, nil
}

func (reader *packetReader) uint32() (uint32, error) {
	if len(reader.bytes)-reader.index < 4 {
		return 0, fmt.Errorf("unexpected end of packet")
	}
	value := binary.LittleEndian.Uint32(reader.bytes[reader.index:])
	reader.index += 4
	return value, nil
}

func (reader *packetReader) string() (string, error) {
	start := reader.index
	for reader.index < len(reader.bytes) {
		if reader.bytes[reader.index] == 0 {
			value := string(reader.bytes[start:reader.index])
			reader.index++
			return value, nil
		}
		reader.index++
	}
	return "", fmt.Errorf("unterminated string")
}
