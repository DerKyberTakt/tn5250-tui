package tn5250

import (
	"errors"
	"fmt"
)

// GDSType is the General Data Stream record type of 5250 (RFC 1205 section 3).
const GDSType = 0x12a0

// Record header flag bits (first flag byte).
const (
	// FlagERR marks a record that reports an error.
	FlagERR = 0x80
	// FlagATN is the Attention flag (Attn key, out of band).
	FlagATN = 0x40
	// FlagSRQ is the System Request flag.
	FlagSRQ = 0x04
	// FlagTRQ is the Test Request flag.
	FlagTRQ = 0x02
	// FlagHLP is the Help flag.
	FlagHLP = 0x01
)

// Record opcodes.
const (
	// OpNoop is the no-operation opcode (also used for query and screen replies).
	OpNoop = 0x00
	// OpInvite invites the terminal to send input.
	OpInvite = 0x01
	// OpOutputOnly is an output-only record.
	OpOutputOnly = 0x02
	// OpPutGet is a put/get record (data from the host, or an AID response to it).
	OpPutGet = 0x03
	// OpSaveScreen carries a saved screen image.
	OpSaveScreen = 0x04
	// OpRestoreScreen asks for a saved screen image to be restored.
	OpRestoreScreen = 0x05
	// OpReadImmediate is a read-immediate record.
	OpReadImmediate = 0x06
	// OpReadScreen is a read-screen record.
	OpReadScreen = 0x08
	// OpCancelInvite cancels an invite; the terminal echoes it.
	OpCancelInvite = 0x0a
	// OpMessageLightOn turns the message waiting light on.
	OpMessageLightOn = 0x0b
	// OpMessageLightOff turns the message waiting light off.
	OpMessageLightOff = 0x0c
)

// ErrGDS is wrapped by every record parse error.
var ErrGDS = errors.New("gds")

// Record is one parsed GDS record.
type Record struct {
	// Length is the record length from the header (it counts the header itself).
	Length int
	// Flags1 is the first flag byte (FlagERR, FlagATN, ...).
	Flags1 byte
	// Flags2 is the second flag byte.
	Flags2 byte
	// Opcode is the record opcode (OpPutGet, ...).
	Opcode byte
	// Data is the payload after the variable header; it aliases the input slice.
	Data []byte
}

// ParseRecord parses a record (the telnet layer already removed IAC framing).
func ParseRecord(b []byte) (Record, error) {
	if len(b) < 10 {
		return Record{}, fmt.Errorf("%w: record too short (%d bytes)", ErrGDS, len(b))
	}
	length := int(b[0])<<8 | int(b[1])
	typ := int(b[2])<<8 | int(b[3])
	if typ != GDSType {
		return Record{}, fmt.Errorf("%w: unexpected GDS type 0x%x", ErrGDS, typ)
	}
	varHdr := int(b[6])
	if varHdr < 4 {
		return Record{}, fmt.Errorf("%w: invalid variable header length %d", ErrGDS, varHdr)
	}
	start := 6 + varHdr
	if start > len(b) {
		start = len(b)
	}
	return Record{Length: length, Flags1: b[7], Flags2: b[8], Opcode: b[9], Data: b[start:]}, nil
}

// BuildRecord builds a record with the 10-byte header.
func BuildRecord(opcode, flags1 byte, data []byte) []byte {
	total := 10 + len(data)
	out := make([]byte, total)
	out[0] = byte(total >> 8)
	out[1] = byte(total)
	out[2] = 0x12
	out[3] = 0xa0
	out[6] = 0x04
	out[7] = flags1
	out[9] = opcode
	copy(out[10:], data)
	return out
}
