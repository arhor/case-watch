package vaadin

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
)

const (
	ElementFeature        FeatureID = 0
	PropertiesFeature     FeatureID = 1
	ChildrenFeature       FeatureID = 2
	AttributesFeature     FeatureID = 3
	ListenersFeature      FeatureID = 4
	PushFeature           FeatureID = 5
	PushParametersFeature FeatureID = 6
	TextFeature           FeatureID = 7
	ClassesFeature        FeatureID = 11
	StylesFeature         FeatureID = 12
	AdditionalFeature     FeatureID = 19
)

const (
	Attach = "attach"
	Detach = "detach"
	Put    = "put"
	Remove = "remove"
	Splice = "splice"
	Clear  = "clear"
	Empty  = "empty"
)

type NodeID uint64
type FeatureID uint64

// NodeReference distinguishes a map's nodeValue from a numeric scalar value.
type NodeReference struct {
	ID NodeID
}

// Change retains the wire payload. Apply validates required fields and types;
// JSON numbers use float64, matching encoding/json's ordinary object decoding.
type Change map[string]any

// Message keeps synchronization data separate from the state tree. A nil
// SecurityKey means the caller must retain its previous key.
type Message struct {
	SyncID      uint64
	ClientID    uint64
	SecurityKey *string
	Changes     []Change
}

type Bootstrap struct {
	UIID    uint64
	Message Message
}

// DecodeBootstrap extracts JSON objects using a decoder, without executing or
// interpreting the surrounding HTML/JavaScript.
func DecodeBootstrap(data []byte) (Bootstrap, error) {
	uidl, err := bootstrapObject(data, "var uidl =")
	if err != nil {
		return Bootstrap{}, err
	}
	config, err := bootstrapObject(data, "var config =")
	if err != nil {
		return Bootstrap{}, err
	}
	uiID, ok := integer(config["v-uiId"])
	if !ok {
		return Bootstrap{}, errors.New("invalid UI identifier")
	}
	message, err := decodeMessage(uidl)
	if err != nil {
		return Bootstrap{}, err
	}
	return Bootstrap{UIID: uiID, Message: message}, nil
}

// DecodeResponse accepts framed or unframed arrays and preserves message order.
// Empty changes are valid; an empty envelope is not.
func DecodeResponse(data []byte) ([]Message, error) {
	var rawMessages []map[string]any
	data = bytes.TrimPrefix(bytes.TrimSpace(data), []byte("for(;;);"))
	if err := json.Unmarshal(data, &rawMessages); err != nil || len(rawMessages) == 0 {
		return nil, errors.New("invalid RPC response")
	}
	messages := make([]Message, 0, len(rawMessages))
	for _, raw := range rawMessages {
		message, err := decodeMessage(raw)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

// -- Internals ----------------------------------------------------------------

func bootstrapObject(data []byte, marker string) (map[string]any, error) {
	index := bytes.Index(data, []byte(marker))
	if index < 0 {
		return nil, errors.New("bootstrap marker missing")
	}
	var object map[string]any
	if err := json.NewDecoder(bytes.NewReader(data[index+len(marker):])).Decode(&object); err != nil || object == nil {
		return nil, errors.New("invalid bootstrap object")
	}
	return object, nil
}

func decodeMessage(raw map[string]any) (Message, error) {
	syncID, syncOK := integer(raw["syncId"])
	clientID, clientOK := integer(raw["clientId"])
	if !syncOK || !clientOK {
		return Message{}, errors.New("invalid RPC synchronization state")
	}
	message := Message{SyncID: syncID, ClientID: clientID}
	if value, exists := raw["Vaadin-Security-Key"]; exists {
		key, ok := value.(string)
		if !ok {
			return Message{}, errors.New("invalid RPC security metadata")
		}
		message.SecurityKey = &key
	}
	if value, exists := raw["changes"]; exists {
		changes, ok := value.([]any)
		if !ok {
			return Message{}, errors.New("invalid RPC changes")
		}
		for _, rawChange := range changes {
			change, ok := rawChange.(map[string]any)
			if !ok || change == nil {
				return Message{}, errors.New("invalid RPC change")
			}
			message.Changes = append(message.Changes, Change(change))
		}
	}
	return message, nil
}

func integer(value any) (uint64, bool) {
	number, ok := value.(float64)
	if !ok || number < 0 || number > 1<<53-1 || math.IsNaN(number) || math.Trunc(number) != number {
		return 0, false
	}
	return uint64(number), true
}
