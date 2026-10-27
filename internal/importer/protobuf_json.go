package importer

import (
	"bytes"
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func canonicalProtoJSON(message proto.Message, retainWire bool) ([]byte, error) {
	encoded, err := protojson.Marshal(message)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if retainWire {
		wire, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
		if err != nil {
			return nil, err
		}
		// This wire comes from the visibility-filtered clone. It preserves
		// unknown options on retained declarations without restoring @ignore.
		value["x-protobuf-descriptor-wire"] = wire
	}
	return json.Marshal(value)
}
