package device

import "encoding/binary"

// The GPIO character device's v2 ABI (linux/gpio.h, Linux 5.10 and later). The
// request is laid out by hand into a byte buffer rather than mirrored by a Go struct:
// the kernel aligns its 64-bit fields to 8 bytes on every architecture, Go aligns
// them to 4 on 32-bit ARM — the Pi Zero's — and a struct that matched on arm64 would
// silently not on ARMv6. These offsets are the C layout, and the test pins them.
const (
	gpioMaxNameSize = 32
	gpioV2LinesMax  = 64

	// struct gpio_v2_line_request
	reqOffsets         = 0                             // __u32 offsets[64]
	reqConsumer        = reqOffsets + 4*gpioV2LinesMax // char consumer[32]
	reqConfig          = reqConsumer + gpioMaxNameSize // struct gpio_v2_line_config
	reqNumLines        = reqConfig + lineConfigSize    // __u32 num_lines
	reqEventBufferSize = reqNumLines + 4               // __u32 event_buffer_size
	reqFD              = reqEventBufferSize + 4 + 4*5  // after __u32 padding[5]: __s32 fd
	lineRequestSize    = reqFD + 4                     // 592

	// struct gpio_v2_line_config, relative to its start
	cfgFlags       = 0  // __aligned_u64 flags
	cfgNumAttrs    = 8  // __u32 num_attrs
	cfgAttrs       = 32 // after __u32 padding[5]: struct gpio_v2_line_config_attribute attrs[10]
	cfgAttrSize    = 24 // { struct gpio_v2_line_attribute { __u32 id; __u32 padding; union 64 } ; __aligned_u64 mask }
	cfgAttrsMax    = 10
	lineConfigSize = cfgAttrs + cfgAttrSize*cfgAttrsMax // 272

	// struct gpio_v2_line_values
	lineValuesSize = 16 // { __aligned_u64 bits; __aligned_u64 mask; }

	gpioV2LineFlagInput        = 1 << 2
	gpioV2LineFlagOutput       = 1 << 3
	gpioV2LineFlagBiasPullUp   = 1 << 8
	gpioV2LineFlagBiasPullDown = 1 << 9
	gpioV2LineFlagBiasDisabled = 1 << 10

	gpioV2LineAttrIDOutputValues = 2
)

// ioctl request numbers: _IOWR(0xB4, nr, size) is 3<<30 | size<<16 | 0xB4<<8 | nr,
// the encoding of every architecture a Raspberry Pi runs.
func iowr(nr, size uintptr) uintptr { return 3<<30 | size<<16 | 0xB4<<8 | nr }

var (
	gpioV2GetLineIoctl       = iowr(0x07, lineRequestSize)
	gpioV2LineGetValuesIoctl = iowr(0x0E, lineValuesSize)
	gpioV2LineSetValuesIoctl = iowr(0x0F, lineValuesSize)
)

// encodeLineRequest lays out a request for one line. initial is used only for an
// output: it is attached as the line's output value, so the kernel drives it from
// the instant the line is claimed.
func encodeLineRequest(line int, flags uint64, output, initial bool, consumer string) []byte {
	buf := make([]byte, lineRequestSize)
	ne := binary.NativeEndian
	ne.PutUint32(buf[reqOffsets:], uint32(line))
	c := []byte(consumer)
	if len(c) > gpioMaxNameSize-1 {
		c = c[:gpioMaxNameSize-1] // the kernel wants it NUL-terminated
	}
	copy(buf[reqConsumer:], c)
	ne.PutUint64(buf[reqConfig+cfgFlags:], flags)
	if output {
		ne.PutUint32(buf[reqConfig+cfgNumAttrs:], 1)
		attr := reqConfig + cfgAttrs
		ne.PutUint32(buf[attr:], gpioV2LineAttrIDOutputValues)
		var v uint64
		if initial {
			v = 1
		}
		ne.PutUint64(buf[attr+8:], v)  // the values bitmap, bit 0 is the request's only line
		ne.PutUint64(buf[attr+16:], 1) // the mask: the attribute applies to that line
	}
	ne.PutUint32(buf[reqNumLines:], 1)
	return buf
}

// inputFlags maps a bias onto the request's flags.
func inputFlags(b Bias) uint64 {
	f := uint64(gpioV2LineFlagInput)
	switch b {
	case BiasPullUp:
		f |= gpioV2LineFlagBiasPullUp
	case BiasPullDown:
		f |= gpioV2LineFlagBiasPullDown
	default:
		f |= gpioV2LineFlagBiasDisabled
	}
	return f
}

// encodeValues lays out the one-line values a get or set exchanges.
func encodeValues(high bool) []byte {
	buf := make([]byte, lineValuesSize)
	var v uint64
	if high {
		v = 1
	}
	binary.NativeEndian.PutUint64(buf[0:], v)
	binary.NativeEndian.PutUint64(buf[8:], 1)
	return buf
}

// decodeValue reads line 0 out of a values buffer the kernel filled.
func decodeValue(buf []byte) bool { return binary.NativeEndian.Uint64(buf[0:])&1 == 1 }

// decodeFD reads the line's file descriptor out of a request the kernel answered.
func decodeFD(buf []byte) int { return int(int32(binary.NativeEndian.Uint32(buf[reqFD:]))) }
