package device

// Chip is a GPIO controller as the device sees it: a set of lines, each requested
// once, either to drive or to read. Levels here are physical — true is a high wire.
// Which level means "on" is the Device's business, from the output's activeLow.
//
// A requested line is held exclusively until it is closed: the kernel refuses a
// second request for it, so two Worker Instances cannot drive one relay against each
// other. A device Worker therefore has one Instance; scaling does not apply to
// hardware.
type Chip interface {
	// RequestOutput claims a line as an output, driving initial from the moment it
	// is claimed, so the line never shows another level in between.
	RequestOutput(line int, initial bool, consumer string) (OutputLine, error)
	// RequestInput claims a line as an input with the given bias.
	RequestInput(line int, bias Bias, consumer string) (InputLine, error)
	// Close releases the chip. Lines still held are released by the kernel with it.
	Close() error
}

// OutputLine is a claimed output.
type OutputLine interface {
	Set(high bool) error
	Close() error
}

// InputLine is a claimed input.
type InputLine interface {
	Value() (high bool, err error)
	Close() error
}

// Bias is an input's internal resistor.
type Bias int

const (
	BiasNone Bias = iota
	BiasPullUp
	BiasPullDown
)
