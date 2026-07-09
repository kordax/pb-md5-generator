package parser

import (
	"fmt"
	"strings"

	"github.com/kordax/basic-utils/v3/uarray"
	"github.com/pseudomuto/protokit"
	"google.golang.org/protobuf/types/descriptorpb"
)

func mapSlice[T, R any](values []T, mapper func(T) R) []R {
	return uarray.Map(values, mapper)
}

func filter[T any](values []T, predicate func(T) bool) []T {
	return uarray.Filter(values, predicate)
}

func contains[T comparable](needle T, values []T) int {
	return uarray.Contains(values, needle)
}

func containsPredicate[T any](values []T, predicate func(T) bool) (int, *T) {
	return uarray.ContainsPredicate(values, predicate)
}

func MapSlice[T, R any](values []T, mapper func(T) R) []R {
	return mapSlice(values, mapper)
}

func Filter[T any](values []T, predicate func(T) bool) []T {
	return filter(values, predicate)
}

func Contains[T comparable](needle T, values []T) int {
	return contains(needle, values)
}

func ContainsPredicate[T any](values []T, predicate func(T) bool) (int, *T) {
	return containsPredicate(values, predicate)
}

func protoToFieldValueType(d *protokit.FieldDescriptor) ValueType {
	switch d.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_INT64:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_INT32:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_UINT64:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_UINT32:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_SINT64:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_SINT32:
		return ValueTypeInt
	case descriptorpb.FieldDescriptorProto_TYPE_FIXED64:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_FIXED32:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_SFIXED32:
		return ValueTypeFloat
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		return ValueTypeBool
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		if strings.Contains(strings.ToLower(d.GetName()), "uuid") {
			return ValueTypeUUID
		}
		if strings.Contains(strings.ToLower(d.GetName()), "email") {
			return ValueTypeEmail
		}
		if strings.Contains(strings.ToLower(d.GetName()), "phone") {
			return ValueTypePhone
		}
		if strings.Contains(strings.ToLower(d.GetName()), "password") {
			return ValueTypePassword
		}
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		return ValueTypeString
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		return ValueTypeEnum
	case descriptorpb.FieldDescriptorProto_TYPE_GROUP:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE:
		return ValueTypeStruct
	}

	return ValueTypeString
}

func mapStringToValueType(customType string) (ValueType, error) {
	switch strings.ToLower(customType) {
	case "int":
		return ValueTypeInt, nil
	case "uint":
		return ValueTypeUInt, nil
	case "float":
		return ValueTypeFloat, nil
	case "bool":
		return ValueTypeBool, nil
	case "string":
		return ValueTypeString, nil
	case "enum":
		return ValueTypeEnum, nil
	case "jwt":
		return ValueTypeJWT, nil
	case "uuid":
		return ValueTypeUUID, nil
	case "struct":
		return ValueTypeStruct, nil
	case "email":
		return ValueTypeEmail, nil
	case "phone":
		return ValueTypePhone, nil
	case "password":
		return ValueTypePassword, nil
	default:
		return 0, fmt.Errorf("unknown custom type provided: %s", customType)
	}
}

func MapStringToValueType(customType string) (ValueType, error) {
	return mapStringToValueType(customType)
}

func ProtoToFieldValueType(d *protokit.FieldDescriptor) ValueType {
	return protoToFieldValueType(d)
}
