package engine

import (
	"encoding/json"
	"fmt"
	"math/rand"
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
	r := rand.New(rand.NewSource(time.Now().UnixNano() + rand.New(rand.NewSource(time.Now().UnixNano())).Int63()))
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
		return int64WithinRange(r, int64(minVal.OrElse(0)), int64(maxVal.OrElse(1000000))), nil
	case ValueTypeFloat:
		if value.Present() {
			return strconv.ParseFloat(*value.Get(), 64)
		}
		return minVal.OrElse(0.0) + r.Float64()*(maxVal.OrElse(1000000)-minVal.OrElse(0.0)), nil
	case ValueTypeUInt:
		if value.Present() {
			return strconv.ParseUint(*value.Get(), 10, 64)
		}
		return uint64WithinRange(r, uint64(minVal.OrElse(0)), uint64(maxVal.OrElse(1000000))), nil
	case ValueTypeEmail:
		fallthrough
	case ValueTypeString:
		if value.Present() {
			return value, nil
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
		phone := "+" + strconv.Itoa(int(int64WithinRange(r, 0, 1010)))
		phone += "." + strconv.Itoa(int(int64WithinRange(r, 1000000000, 9999999999)))

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

			return values[rand.Intn(l)].d.GetName(), nil
		}

		return nil, nil
	case ValueTypeStruct:
		return nil, fmt.Errorf("cannot generate code from struct, you need to convert it to the field first")
	default:
		return nil, fmt.Errorf("unsupported value type received: field '%s', message '%s', package: %s", *field.d.Name, *field.d.Message.Name, field.d.Message.GetPackage())
	}
}

func int64WithinRange(r *rand.Rand, min, max int64) int64 {
	return min + r.Int63n(max)
}

func uint64WithinRange(r *rand.Rand, min, max uint64) uint64 {
	return min + r.Uint64()*(max-min)
}
