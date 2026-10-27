package engine

import (
	cryptoRand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	mathRand "math/rand"
	"strconv"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/goombaio/namegenerator"
	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/kordax/pb-md5-generator/engine/password"
)

// DefaultCodegenSeed is the reproducible seed used by NewCodegenerator.
const DefaultCodegenSeed int64 = 1

type Codegenerator struct {
	namegen namegenerator.Generator
	random  *mathRand.Rand
	passgen *password.Generator
}

func NewCodegenerator() *Codegenerator {
	return NewCodegeneratorWithSeed(DefaultCodegenSeed)
}

// NewCodegeneratorWithSeed creates a generator whose examples are repeatable for the same seed.
func NewCodegeneratorWithSeed(seed int64) *Codegenerator {
	random := mathRand.New(mathRand.NewSource(seed)) // #nosec G404 -- generated examples require reproducibility, not cryptographic randomness.
	return &Codegenerator{
		namegen: namegenerator.NewNameGenerator(seed),
		random:  random,
		passgen: password.NewGeneratorWithReader(random, 1, 7, 5, 1),
	}
}

func (g *Codegenerator) Generate(files []ParsedFile, message *Message) (*md.Codeblock, error) {
	if message.code.Present() {
		result := md.NewCodeblockBuilder().Text(message.code.Get().Right).Language(exampleLanguage(message.code.Get().Left)).Build()
		return result, nil
	} else if message.autocode.Present() {
		autocode, err := g.generateFromMessage(files, message, nil)
		if err != nil {
			return nil, err
		}
		result := md.NewCodeblockBuilder().Text(autocode).Language(exampleLanguage(message.autocode.Get().syntax)).Build()
		return result, nil
	} else {
		return nil, fmt.Errorf("received message entry with both code and autocode flags missing")
	}
}

func (g *Codegenerator) generateFromMessage(files []ParsedFile, message *Message, js map[string]any) (string, error) {
	if js == nil {
		js = make(map[string]any)
	}
	transactionID, err := g.uuidString()
	if err != nil {
		return "", err
	}
	body, err := newExampleContext(g, files).messageValue(message, 0)
	if err != nil {
		return "", err
	}
	js["trx"] = transactionID
	js[message.m.GetName()] = body
	if message.autocode.Present() && message.autocode.Get().syntax == SyntaxXml {
		return marshalExampleXML(js)
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
		return g.signedExample(field, field.flags.OrElse(FieldFlags{}))
	case ValueTypeFloat:
		if value.Present() {
			return strconv.ParseFloat(*value.Get(), 64)
		}
		return float64WithinRangeFrom(g.random, minVal.OrElse(0), maxVal.OrElse(1000000))
	case ValueTypeUInt:
		return g.unsignedExample(field, field.flags.OrElse(FieldFlags{}))
	case ValueTypeBool:
		if value.Present() {
			return strconv.ParseBool(*value.Get())
		}
		index, err := cryptoIndexFrom(g.random, 2)
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
		suffix := ""
		if field.valueType == ValueTypeEmail {
			suffix = "@email.com"
			if maxLen.Present() && *maxLen.Get() < utf8.RuneCountInString(suffix)+1 {
				if *maxLen.Get() < 3 {
					return nil, fmt.Errorf("email max_len must allow at least three characters")
				}
				suffix = "@b"
			}
		}
		if maxLen.Present() {
			limit := *maxLen.Get() - utf8.RuneCountInString(suffix)
			if limit < 0 {
				return nil, fmt.Errorf("max_len cannot be negative")
			}
			if utf8.RuneCountInString(str) > limit {
				str = string([]rune(str)[:limit])
			}
		}
		return str + suffix, nil
	case ValueTypePhone:
		if value.Present() {
			return *value.Get(), nil
		}
		//+NNN.NNNNNNNNNN
		prefix, err := int64WithinRangeFrom(g.random, 0, 1010)
		if err != nil {
			return nil, err
		}
		number, err := int64WithinRangeFrom(g.random, 1000000000, 9999999999)
		if err != nil {
			return nil, err
		}
		phone := "+" + strconv.FormatInt(prefix, 10)
		phone += "." + strconv.FormatInt(number, 10)

		return phone, nil
	case ValueTypeJWT:
		if value.Present() {
			return *value.Get(), nil
		}
		return "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJmaXh0dXJlIn0.signature", nil
	case ValueTypePassword:
		if value.Present() {
			return *value.Get(), nil
		}
		return g.passgen.GetPassword(), nil
	case ValueTypeUUID:
		if value.Present() {
			return *value.Get(), nil
		}
		return g.uuidString()
	case ValueTypeEnum:
		var enum *Enum
		for _, file := range files {
			enum = findExampleEnum(file.entries, field.d.GetTypeName())
			if enum != nil {
				break
			}
		}

		if enum != nil {
			values := enum.values
			if value.Present() {
				for _, candidate := range values {
					if candidate.d.GetName() == *value.Get() {
						return *value.Get(), nil
					}
				}
				return nil, fmt.Errorf("unknown enum example %q", *value.Get())
			}
			l := len(values)
			index, err := cryptoIndexFrom(g.random, l)
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

func (g *Codegenerator) uuidString() (string, error) {
	value, err := uuid.NewRandomFromReader(g.random)
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

func int64WithinRange(min, max int64) (int64, error) {
	return int64WithinRangeFrom(cryptoRand.Reader, min, max)
}

func int64WithinRangeFrom(reader io.Reader, min, max int64) (int64, error) {
	if max <= min {
		return min, nil
	}
	minimum := big.NewInt(min)
	diff := new(big.Int).Sub(big.NewInt(max), minimum)
	value, err := cryptoRand.Int(reader, diff)
	if err != nil {
		return 0, err
	}
	return value.Add(value, minimum).Int64(), nil
}

func uint64WithinRange(min, max uint64) (uint64, error) {
	return uint64WithinRangeFrom(cryptoRand.Reader, min, max)
}

func uint64WithinRangeFrom(reader io.Reader, min, max uint64) (uint64, error) {
	if max <= min {
		return min, nil
	}
	diff := new(big.Int).SetUint64(max - min)
	value, err := cryptoRand.Int(reader, diff)
	if err != nil {
		return 0, err
	}
	return min + value.Uint64(), nil
}

func float64WithinRange(min, max float64) (float64, error) {
	return float64WithinRangeFrom(cryptoRand.Reader, min, max)
}

func float64WithinRangeFrom(reader io.Reader, min, max float64) (float64, error) {
	if max <= min {
		return min, nil
	}
	maxUint64 := ^uint64(0)
	value, err := uint64WithinRangeFrom(reader, 0, maxUint64)
	if err != nil {
		return 0, err
	}
	ratio := float64(value) / float64(maxUint64)
	return (1-ratio)*min + ratio*max, nil
}

func cryptoIndex(max int) (int, error) {
	return cryptoIndexFrom(cryptoRand.Reader, max)
}

func cryptoIndexFrom(reader io.Reader, max int) (int, error) {
	if max <= 0 {
		return 0, fmt.Errorf("max should be positive")
	}
	value, err := cryptoRand.Int(reader, big.NewInt(int64(max)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}
