package domain

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed run.schema.json
var eventSchema string

//go:embed read.schema.json
var toolSchema string

//go:embed actions.schema.json
var actionSchema string

var compiledEventSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	var document any
	if err := json.NewDecoder(strings.NewReader(eventSchema)).Decode(&document); err != nil {
		return nil, err
	}
	if err := compiler.AddResource("urn:flowdesk:events:v1", document); err != nil {
		return nil, err
	}
	return compiler.Compile("urn:flowdesk:events:v1")
})

func ValidateEvent(event any) error {
	schema, err := compiledEventSchema()
	if err != nil {
		return err
	}
	return schema.Validate(event)
}

func IsTerminal(status string) bool {
	return status == RunSucceeded || status == RunFailed || status == RunCancelled || status == RunExpired
}

var compiledToolSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	var document any
	if err := json.NewDecoder(strings.NewReader(toolSchema)).Decode(&document); err != nil {
		return nil, err
	}
	if err := compiler.AddResource("urn:flowdesk:tools:v1", document); err != nil {
		return nil, err
	}
	return compiler.Compile("urn:flowdesk:tools:v1")
})

func ValidateTool(request any) error {
	schema, err := compiledToolSchema()
	if err != nil {
		return err
	}
	return schema.Validate(request)
}

var compiledActionSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	var document any
	if err := json.NewDecoder(strings.NewReader(actionSchema)).Decode(&document); err != nil {
		return nil, err
	}
	if err := compiler.AddResource("urn:flowdesk:actions:v1", document); err != nil {
		return nil, err
	}
	return compiler.Compile("urn:flowdesk:actions:v1")
})

func ValidateAction(request any) error {
	schema, err := compiledActionSchema()
	if err != nil {
		return err
	}
	return schema.Validate(request)
}
