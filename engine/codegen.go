package engine

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/goombaio/namegenerator"
	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/kordax/pb-md5-generator/engine/password"
	"github.com/rs/zerolog/log"
)

type Syntax int

const (
	SyntaxJson Syntax = iota
	SyntaxXml
)

type ValueType int

const (
	ValueTypeInt ValueType = iota
	ValueTypeUInt
	ValueTypeFloat
	ValueTypeBool
	ValueTypeString
	ValueTypeEnum
	ValueTypeJWT
	ValueTypeUUID
	ValueTypeStruct
	ValueTypeEmail
	ValueTypePhone
	ValueTypePassword
)

var passGen = password.NewGenerator(1, 7, 5, 1)

type Codegenerator struct {
	namegen namegenerator.Generator
}

func NewCodegenerator() *Codegenerator {
	return &Codegenerator{
		namegen: namegenerator.NewNameGenerator(time.Now().UnixNano()),
	}
}

func (g *Codegenerator) Generate(files []ParsedFile, message *Message) (*md.Codeblock, error) {
	if message.code.Present() {
		result := md.NewCodeblockBuilder().Text(message.code.Get().Right).Build()
		return result, nil
	} else if message.autocode.Present() {
		autocode, err := g.generateFromMessage(files, message, nil)
		if err != nil {
			return nil, err
		}
		result := md.NewCodeblockBuilder().Text(autocode).Build()
		return result, nil
	} else {
		return nil, fmt.Errorf("received message entry with both code and autocode flags missing")
	}
}

func (g *Codegenerator) generateFromMessage(files []ParsedFile, message *Message, js map[string]any) (string, error) {
	if js == nil {
		js = make(map[string]any)
	}
	js["trx"] = uuid.NewString()
	js[message.m.GetName()] = map[string]any{}
	jsMsg := js[message.m.GetName()].(map[string]any)
	for _, field := range message.fields {
		if field.isMsg == nil {
			value, err := g.generateFromField(files, field)
			if err != nil {
				return "", err
			}
			jsMsg[field.d.GetName()] = value
		} else {
			jsMsg[field.d.GetName()] = map[string]any{}
			return g.generateFromMessage(nil, field.isMsg, js[field.d.GetName()].(map[string]any))
		}
	}
	res, err := json.MarshalIndent(js, "", "\t")
	return string(res), err
}

func (g *Codegenerator) generateFromField(files []ParsedFile, field MessageField) (any, error) {
	minVal := field.flags.OrElse(FieldFlags{}).GetMin()
	maxVal := field.flags.OrElse(FieldFlags{}).GetMax()
	maxLen := field.flags.OrElse(FieldFlags{}).GetMaxLength()
	value := field.flags.OrElse(FieldFlags{}).GetValue()
	customType := field.flags.OrElse(FieldFlags{}).GetCustomType()
	field.valueType = customType.OrElse(field.ValueType())
	switch field.valueType {
	case ValueTypeInt:
		if value.Present() {
			return strconv.ParseInt(*value.Get(), 10, 64)
		}
		return int64WithinRange(int64(minVal.OrElse(0)), int64(maxVal.OrElse(1000000)))
	case ValueTypeFloat:
		if value.Present() {
			return strconv.ParseFloat(*value.Get(), 64)
		}
		return float64WithinRange(minVal.OrElse(0), maxVal.OrElse(1000000))
	case ValueTypeUInt:
		if value.Present() {
			return strconv.ParseUint(*value.Get(), 10, 64)
		}
		return uint64WithinRange(uint64(minVal.OrElse(0)), uint64(maxVal.OrElse(1000000)))
	case ValueTypeBool:
		if value.Present() {
			return strconv.ParseBool(*value.Get())
		}
		index, err := cryptoIndex(2)
		if err != nil {
			return nil, err
		}
		return index == 1, nil
	case ValueTypeEmail:
		fallthrough
	case ValueTypeString:
		if value.Present() {
			return *value.Get(), nil
		}
		str := g.namegen.Generate()
		if maxLen.Present() {
			count := utf8.RuneCountInString(str)
			if count > *maxLen.Get() {
				return str[:*maxLen.Get()], nil
			}
		}

		if field.valueType == ValueTypeEmail {
			return str + "@email.com", nil
		}

		return str, nil
	case ValueTypePhone:
		//+NNN.NNNNNNNNNN
		prefix, err := int64WithinRange(0, 1010)
		if err != nil {
			return nil, err
		}
		number, err := int64WithinRange(1000000000, 9999999999)
		if err != nil {
			return nil, err
		}
		phone := "+" + strconv.FormatInt(prefix, 10)
		phone += "." + strconv.FormatInt(number, 10)

		return phone, nil
	case ValueTypePassword:
		return passGen.GetPassword(), nil
	case ValueTypeUUID:
		return uuid.NewString(), nil
	case ValueTypeEnum:
		var enum *Enum
		for _, file := range files {
			for _, entry := range file.entries {
				if entry.enum != nil {
					log.Info().Msgf("reading enum descriptor: % s", entry.enum.e.GetName())
					if "."+entry.enum.e.GetFullName() == field.d.GetTypeName() {
						enum = entry.enum
					}
					if enum != nil {
						break
					}
				}
			}
		}

		if enum != nil {
			values := enum.values
			l := len(values)
			index, err := cryptoIndex(l)
			if err != nil {
				return nil, err
			}

			return values[index].d.GetName(), nil
		}

		return nil, nil
	case ValueTypeStruct:
		return nil, fmt.Errorf("cannot generate code from struct, you need to convert it to the field first")
	default:
		fieldName := field.d.GetName()
		messageName := ""
		packageName := ""
		if field.d.Message != nil {
			messageName = field.d.Message.GetName()
			packageName = field.d.Message.GetPackage()
		}
		return nil, fmt.Errorf("unsupported value type received: field '%s', message '%s', package: %s", fieldName, messageName, packageName)
	}
}

func int64WithinRange(min, max int64) (int64, error) {
	if max <= min {
		return min, nil
	}
	diff := max - min
	value, err := rand.Int(rand.Reader, big.NewInt(diff))
	if err != nil {
		return 0, err
	}
	return min + value.Int64(), nil
}

func uint64WithinRange(min, max uint64) (uint64, error) {
	if max <= min {
		return min, nil
	}
	diff := new(big.Int).SetUint64(max - min)
	value, err := rand.Int(rand.Reader, diff)
	if err != nil {
		return 0, err
	}
	return min + value.Uint64(), nil
}

func float64WithinRange(min, max float64) (float64, error) {
	if max <= min {
		return min, nil
	}
	maxUint64 := ^uint64(0)
	value, err := uint64WithinRange(0, maxUint64)
	if err != nil {
		return 0, err
	}
	ratio := float64(value) / float64(maxUint64)
	return min + ratio*(max-min), nil
}

func cryptoIndex(max int) (int, error) {
	if max <= 0 {
		return 0, fmt.Errorf("max should be positive")
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}
