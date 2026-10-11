package hff

import "github.com/ornew/hff/internal/core"
import "context"

type Input core.Input
type ValidationConfig = core.ValidationConfig

func ValidateInput(ctx context.Context, schema Type, value Value, config ValidationConfig) (Input, error) {
	input, err := core.ValidateInput(ctx, core.Type(schema), core.Value(value), config)
	return Input(input), err
}

func (i Input) Value() Value           { return Value(core.Input(i).Value()) }
func (i Input) Schema() Type           { return Type(core.Input(i).Schema()) }
func (i Input) Compatible(t Type) bool { return core.Input(i).Compatible(core.Type(t)) }
