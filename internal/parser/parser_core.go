package parser

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/pseudomuto/protokit"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/types/pluginpb"
)

func NewDescriptorParser(request *pluginpb.CodeGeneratorRequest) *DescriptorParser {
	cmdLine := request.GetParameter()
	params := strings.Split(cmdLine, ";")
	matchedFiles := make(map[string]*os.File)
	for _, f := range request.GetFileToGenerate() {
		pathParams := filter(params, func(v string) bool {
			match, _ := regexp.MatchString("M.*proto=.+", v)
			return match
		})
		paths := mapSlice(pathParams, func(v string) Pair[string, string] {
			split := strings.Split(v, "=")
			return Pair[string, string]{Left: split[0], Right: split[1]}
		})
		if _, rawPath := containsPredicate(paths, func(v Pair[string, string]) bool {
			return strings.Trim(v.Left, "M ") == path.Base(f)
		}); rawPath == nil {
			panic(fmt.Errorf("no path provided for file: %s", f))
		} else {
			fullPath := path.Join(rawPath.Right, f)
			lstat, err := os.Stat(fullPath)
			if err != nil {
				panic(fmt.Errorf("no file found, even though matched path was provided, path: %s, err: %s", fullPath, err.Error()))
			}
			file, err := os.OpenFile(fullPath, os.O_RDONLY, lstat.Mode()) // #nosec G304 -- proto source path comes from the compiler request.
			if err != nil {
				panic(fmt.Errorf("failed to open file: %s, path: %s, err: %s", f, fullPath, err.Error()))
			}
			matchedFiles[f] = file
		}
	}

	return &DescriptorParser{
		descriptors:  protokit.ParseCodeGenRequest(request),
		matchedFiles: matchedFiles,
		readOffsets:  make(map[string]int),
		payload:      make(map[string]string),
	}
}

func (p *DescriptorParser) Parse() ([]ParsedFile, error) {
	result := make([]ParsedFile, 0)
	sort.Slice(p.descriptors, func(i, j int) bool {
		return p.descriptors[i].GetName()[0] < p.descriptors[j].GetName()[0]
	})
	msgInd := 0
	enumInd := 0
	for i, descriptor := range p.descriptors {
		entries := make([]Entry, 0)
		log.Info().Msgf("parsing file '%s' to a document", descriptor.GetName())
		log.Info().Msgf("%d messages", len(descriptor.GetMessages()))
		_, ignore, _ := p.getMarker(descriptor, IgnoreFileMarker)
		if ignore != -1 {
			log.Warn().Msgf("ignoring file '%s'", descriptor.GetName())
			continue
		}
		title, _, err := p.getMarker(descriptor, TitleMarker)
		log.Info().Msgf("title: %s", title)
		if err != nil {
			return nil, err
		}
		var header string
		var headerIndex int

		for _, message := range descriptor.GetMessages() {
			h, hi, err := p.nextMarker(descriptor, HeaderMarker)
			if err != nil {
				return nil, err
			}
			sourceIndex := p.getMessageSourceIndex(descriptor, message)
			hdrValue := ""
			if h != "" {
				header = h
				headerIndex = hi
			}
			if headerIndex < sourceIndex {
				hdrValue = header
			}
			msg, err := p.parseMessage(message, hdrValue)
			if err != nil {
				return nil, err
			}
			if contains(IgnoreMarker, msg.flags) != -1 {
				log.Warn().Msgf("ignoring message '%s'", message.GetName())
				continue
			}
			entries = append(entries, Entry{
				index: msgInd,
				t:     EntryTypeMessage,
				msg:   msg,
			})
			msgInd++
		}

		for _, enum := range descriptor.GetEnums() {
			en, err := p.parseEnum(enum)
			if err != nil {
				return nil, err
			}
			if contains(IgnoreMarker, en.flags) != -1 {
				log.Warn().Msgf("ignoring enum '%s'", enum.GetName())
				continue
			}

			entries = append(entries, Entry{
				index: enumInd,
				t:     EntryTypeEnum,
				enum:  en,
			})
			enumInd++
		}

		parsedFile := ParsedFile{
			index:    i,
			filename: descriptor.GetName(),
			title:    title,
			entries:  entries,
		}
		result = append(result, parsedFile)
	}

	return result, nil
}

func (p *DescriptorParser) parseMessage(descriptor *protokit.Descriptor, header string) (*Message, error) {
	log.Debug().Msgf("parsing message: %s", descriptor.GetName())
	result := &Message{
		m:      descriptor,
		header: header,
	}
	result.description = p.parseMessageDescription(descriptor)
	result.flags = p.parseMessageFlags(descriptor)
	autocode, err := p.parseAutocode(descriptor)
	if err != nil {
		return nil, p.messageError(descriptor, err)
	}
	result.autocode = OptionFromPtr(autocode)
	if autocode == nil {
		code, err := p.parseCode(descriptor)
		if err != nil {
			return nil, p.messageError(descriptor, err)
		}
		result.code = OptionFromPtr(code)
	}

	for _, f := range descriptor.GetMessageFields() {
		field, err := p.parseField(f, descriptor)
		if err != nil {
			return nil, err
		}
		if flags := field.flags.Get(); flags != nil {
			if contains(IgnoreMarker, flags.other) != -1 {
				log.Warn().Msgf("ignoring field '%s'", f.GetName())
				continue
			}
		}

		result.fields = append(result.fields, *field)
	}

	for i, d := range descriptor.GetMessages() {
		nestedMsg, err := p.parseMessage(d, header)
		if err != nil {
			return nil, err
		}

		result.entries = append(result.entries, Entry{
			index: i,
			t:     EntryTypeMessage,
			msg:   nestedMsg,
		})
	}

	return result, nil
}

func (p *DescriptorParser) parseEnum(descriptor *protokit.EnumDescriptor) (*Enum, error) {
	log.Debug().Msgf("parsing enum: %s", descriptor.GetName())
	result := &Enum{
		e: descriptor,
	}
	result.description = p.parseEnumDescription(descriptor)
	result.flags = p.parseEnumFlags(descriptor)
	for _, e := range result.e.GetValues() {
		value, err := p.parseEnumValue(e, descriptor)
		if err != nil {
			return nil, err
		}

		result.values = append(result.values, *value)
	}

	return result, nil
}

func (p *DescriptorParser) parseField(descriptor *protokit.FieldDescriptor, m *protokit.Descriptor) (*MessageField, error) {
	log.Debug().Msgf("parsing message field: %s", descriptor.GetFullName())
	vt := protoToFieldValueType(descriptor)
	description := p.parseFieldDescription(descriptor)
	flags, err := p.parseFieldFlags(descriptor)
	if err != nil {
		return nil, p.fieldError(descriptor, err)
	}

	return NewMessageField(descriptor, m, description, vt, flags), nil
}

func (p *DescriptorParser) parseEnumValue(descriptor *protokit.EnumValueDescriptor, e *protokit.EnumDescriptor) (*EnumField, error) {
	log.Debug().Msgf("parsing message field: %s", descriptor.GetFullName())
	description := p.parseEnumValueDescription(descriptor)
	flags, err := p.parseEnumValueFlags(descriptor)
	if err != nil {
		return nil, p.enumValueError(descriptor, err)
	}

	return &EnumField{
		description: description,
		d:           descriptor,
		flags:       flags,
	}, nil
}
