package device

import (
	"encoding/binary"
	"strings"
	"testing"
)

// The ABI is pinned to the numbers linux/gpio.h produces, not to the constants this
// package derives them from: a slip in the derivation must fail here, not on a Pi.
func TestTheGPIOv2LayoutMatchesTheKernel(t *testing.T) {
	if lineRequestSize != 592 {
		t.Errorf("sizeof(struct gpio_v2_line_request) = %d, want 592", lineRequestSize)
	}
	if lineConfigSize != 272 {
		t.Errorf("sizeof(struct gpio_v2_line_config) = %d, want 272", lineConfigSize)
	}
	if reqConfig != 288 || reqNumLines != 560 || reqFD != 588 {
		t.Errorf("offsets: config %d, num_lines %d, fd %d; want 288, 560, 588", reqConfig, reqNumLines, reqFD)
	}
	for name, got := range map[string]uintptr{
		"GPIO_V2_GET_LINE_IOCTL":        gpioV2GetLineIoctl,
		"GPIO_V2_LINE_GET_VALUES_IOCTL": gpioV2LineGetValuesIoctl,
		"GPIO_V2_LINE_SET_VALUES_IOCTL": gpioV2LineSetValuesIoctl,
	} {
		want := map[string]uintptr{
			"GPIO_V2_GET_LINE_IOCTL":        0xC250B407,
			"GPIO_V2_LINE_GET_VALUES_IOCTL": 0xC010B40E,
			"GPIO_V2_LINE_SET_VALUES_IOCTL": 0xC010B40F,
		}[name]
		if got != want {
			t.Errorf("%s = %#x, want %#x", name, got, want)
		}
	}
}

func TestAnOutputRequestCarriesItsInitialLevel(t *testing.T) {
	ne := binary.NativeEndian
	buf := encodeLineRequest(17, gpioV2LineFlagOutput, true, true, "atlas")
	if len(buf) != 592 {
		t.Fatalf("len = %d", len(buf))
	}
	if ne.Uint32(buf[0:]) != 17 {
		t.Error("offsets[0] is not the line")
	}
	if string(buf[256:261]) != "atlas" || buf[261] != 0 {
		t.Errorf("consumer = %q", buf[256:262])
	}
	if ne.Uint64(buf[288:]) != gpioV2LineFlagOutput {
		t.Error("config.flags is not OUTPUT")
	}
	if ne.Uint32(buf[296:]) != 1 {
		t.Error("config.num_attrs is not 1")
	}
	if ne.Uint32(buf[320:]) != gpioV2LineAttrIDOutputValues || ne.Uint64(buf[328:]) != 1 || ne.Uint64(buf[336:]) != 1 {
		t.Errorf("attrs[0] = id %d, values %d, mask %d", ne.Uint32(buf[320:]), ne.Uint64(buf[328:]), ne.Uint64(buf[336:]))
	}
	if ne.Uint32(buf[560:]) != 1 {
		t.Error("num_lines is not 1")
	}

	low := encodeLineRequest(17, gpioV2LineFlagOutput, true, false, "atlas")
	if ne.Uint64(low[328:]) != 0 || ne.Uint64(low[336:]) != 1 {
		t.Error("a low initial level still masks the line, with its bit clear")
	}
}

func TestAnInputRequestCarriesNoOutputValues(t *testing.T) {
	ne := binary.NativeEndian
	buf := encodeLineRequest(27, inputFlags(BiasPullUp), false, false, strings.Repeat("x", 40))
	if ne.Uint32(buf[296:]) != 0 {
		t.Error("an input has no attributes")
	}
	if ne.Uint64(buf[288:]) != gpioV2LineFlagInput|gpioV2LineFlagBiasPullUp {
		t.Errorf("flags = %#x", ne.Uint64(buf[288:]))
	}
	if buf[256+31] != 0 || buf[256+30] != 'x' {
		t.Error("a long consumer is cut to leave its terminating NUL")
	}
}

func TestInputFlags(t *testing.T) {
	for b, want := range map[Bias]uint64{
		BiasNone:     gpioV2LineFlagInput | gpioV2LineFlagBiasDisabled,
		BiasPullUp:   gpioV2LineFlagInput | gpioV2LineFlagBiasPullUp,
		BiasPullDown: gpioV2LineFlagInput | gpioV2LineFlagBiasPullDown,
	} {
		if got := inputFlags(b); got != want {
			t.Errorf("inputFlags(%d) = %#x, want %#x", b, got, want)
		}
	}
}

func TestValuesAndFD(t *testing.T) {
	if !decodeValue(encodeValues(true)) || decodeValue(encodeValues(false)) {
		t.Fatal("a value does not survive its encoding")
	}
	if binary.NativeEndian.Uint64(encodeValues(false)[8:]) != 1 {
		t.Fatal("the mask selects line 0")
	}
	buf := make([]byte, lineRequestSize)
	binary.NativeEndian.PutUint32(buf[reqFD:], 7)
	if decodeFD(buf) != 7 {
		t.Fatal("the fd is read from its offset")
	}
}
