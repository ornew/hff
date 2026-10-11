package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Timestamp("created"), schema.String("elapsed"))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		log.Fatal(err)
	}
	record := hff.Object(map[string]hff.Value{
		"created": hff.Timestamp(time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)), "elapsed": hff.String("1h30m"),
	})
	for _, query := range []string{
		`created:after("2026-10-01T00:00:00Z":timestamp)`,
		`created:hours("+09:00")=18 elapsed:duration:minutes>=60`,
		`created:add("30m":duration):string("UTC")="2026-10-11T10:00:00Z"`,
	} {
		program, err := e.Compile(ctx, query)
		if err != nil {
			log.Fatal(err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			log.Fatal(err)
		}
		yes, err := filter.Match(ctx, record)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s -> %t\n", query, yes)
	}
}
