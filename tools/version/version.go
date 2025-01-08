package main

import (
	"fmt"

	lightstepreceiver "github.com/zalando/otelcol-lightstep-receiver"
)

func main() {
	fmt.Println(lightstepreceiver.Version())
}
