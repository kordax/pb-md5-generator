package engine

import (
	"testing"

	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestCodegeneratorSeedIsDeterministic(t *testing.T) {
	message := deterministicMessageFixture()

	first, err := NewCodegeneratorWithSeed(42).generateFromMessage(nil, message, nil)
	require.NoError(t, err)
	second, err := NewCodegeneratorWithSeed(42).generateFromMessage(nil, message, nil)
	require.NoError(t, err)
	other, err := NewCodegeneratorWithSeed(43).generateFromMessage(nil, message, nil)
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.NotEqual(t, first, other)
}

func TestCodegeneratorDefaultSeedIsStable(t *testing.T) {
	message := deterministicMessageFixture()

	first, err := NewCodegenerator().generateFromMessage(nil, message, nil)
	require.NoError(t, err)
	second, err := NewCodegenerator().generateFromMessage(nil, message, nil)
	require.NoError(t, err)

	assert.Equal(t, first, second)
}

func deterministicMessageFixture() *Message {
	name := "Example"
	descriptor := &protokit.Descriptor{
		DescriptorProto: &descriptorpb.DescriptorProto{Name: &name},
	}
	fields := []struct {
		name      string
		valueType ValueType
	}{
		{name: "name", valueType: ValueTypeString},
		{name: "count", valueType: ValueTypeInt},
		{name: "ratio", valueType: ValueTypeFloat},
		{name: "enabled", valueType: ValueTypeBool},
		{name: "phone", valueType: ValueTypePhone},
		{name: "password", valueType: ValueTypePassword},
		{name: "id", valueType: ValueTypeUUID},
	}

	messageFields := make([]MessageField, 0, len(fields))
	for _, field := range fields {
		messageFields = append(messageFields, *NewMessageField(
			fieldDescriptor(field.name, descriptorpb.FieldDescriptorProto_TYPE_STRING, descriptor),
			descriptor,
			"",
			field.valueType,
			nil,
		))
	}
	return &Message{
		m:        descriptor,
		fields:   messageFields,
		autocode: Some(AutocodeOpt{syntax: SyntaxJson}),
	}
}
