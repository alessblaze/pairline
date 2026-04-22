package turncontrol

import "encoding/json"

const codecName = "json"

type jsonCodec struct{}

var JSONCodec = jsonCodec{}

func (jsonCodec) Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func (jsonCodec) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

func (jsonCodec) Name() string {
	return codecName
}
