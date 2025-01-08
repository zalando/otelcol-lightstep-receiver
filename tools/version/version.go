package main

import (
	"fmt"

	lightstepreceiver "github.bus.zalan.do/logging/otelcol-lightstep-receiver"
)

func main() {
	fmt.Println(lightstepreceiver.Version())
}
