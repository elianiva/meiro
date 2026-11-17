package youtube

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
)

type jsonMember struct {
	key   string
	value any
}

type jsonObject struct {
	members []jsonMember
}

func (o *jsonObject) get(key string) any {
	if o == nil {
		return nil
	}
	for index := range o.members {
		if o.members[index].key == key {
			return o.members[index].value
		}
	}
	return nil
}

func (o *jsonObject) has(key string) bool {
	if o == nil {
		return false
	}
	for index := range o.members {
		if o.members[index].key == key {
			return true
		}
	}
	return false
}

func (o *jsonObject) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, member := range o.members {
		if index > 0 {
			buffer.WriteByte(',')
		}
		key, err := json.Marshal(member.key)
		if err != nil {
			return nil, err
		}
		buffer.Write(key)
		buffer.WriteByte(':')
		value, err := json.Marshal(member.value)
		if err != nil {
			return nil, err
		}
		buffer.Write(value)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

func decodeResponse(raw json.RawMessage) any {
	parser := jsonParser{
		decoder: jsontext.NewDecoder(bytes.NewReader(raw), jsontext.AllowDuplicateNames(true)),
		keys:    make(map[string]string, 64),
	}
	value, err := parser.read()
	if err != nil {
		return nil
	}
	return value
}

type jsonParser struct {
	decoder  *jsontext.Decoder
	keys     map[string]string
	members  []jsonMember
	elements []any
}

func (p *jsonParser) read() (any, error) {
	switch p.decoder.PeekKind() {
	case jsontext.KindBeginObject:
		return p.readObject()
	case jsontext.KindBeginArray:
		return p.readArray()
	default:
		token, err := p.decoder.ReadToken()
		if err != nil {
			return nil, err
		}
		switch token.Kind() {
		case jsontext.KindNull:
			return nil, nil
		case jsontext.KindTrue:
			return true, nil
		case jsontext.KindFalse:
			return false, nil
		case jsontext.KindString:
			return token.String(), nil
		default:
			number, err := token.Float()
			if err != nil {
				return nil, err
			}
			return number, nil
		}
	}
}

func (p *jsonParser) readKey() (string, error) {
	raw, err := p.decoder.ReadValue()
	if err != nil {
		return "", err
	}
	quoted := []byte(raw)
	if bytes.IndexByte(quoted, '\\') >= 0 {
		name, err := jsontext.AppendUnquote(nil, raw)
		if err != nil {
			return "", err
		}
		return string(name), nil
	}
	name := quoted[1 : len(quoted)-1]
	if key, ok := p.keys[string(name)]; ok {
		return key, nil
	}
	key := string(name)
	p.keys[key] = key
	return key, nil
}

func (p *jsonParser) readObject() (*jsonObject, error) {
	if _, err := p.decoder.ReadToken(); err != nil {
		return nil, err
	}
	base := len(p.members)
	for p.decoder.PeekKind() != jsontext.KindEndObject {
		key, err := p.readKey()
		if err != nil {
			return nil, err
		}
		value, err := p.read()
		if err != nil {
			return nil, err
		}
		replaced := false
		for index := base; index < len(p.members); index++ {
			if p.members[index].key == key {
				p.members[index].value = value
				replaced = true
				break
			}
		}
		if !replaced {
			p.members = append(p.members, jsonMember{key: key, value: value})
		}
	}
	if _, err := p.decoder.ReadToken(); err != nil {
		return nil, err
	}
	object := &jsonObject{members: make([]jsonMember, len(p.members)-base)}
	copy(object.members, p.members[base:])
	clear(p.members[base:])
	p.members = p.members[:base]
	return object, nil
}

func (p *jsonParser) readArray() ([]any, error) {
	if _, err := p.decoder.ReadToken(); err != nil {
		return nil, err
	}
	base := len(p.elements)
	for p.decoder.PeekKind() != jsontext.KindEndArray {
		value, err := p.read()
		if err != nil {
			return nil, err
		}
		p.elements = append(p.elements, value)
	}
	if _, err := p.decoder.ReadToken(); err != nil {
		return nil, err
	}
	array := make([]any, len(p.elements)-base)
	copy(array, p.elements[base:])
	clear(p.elements[base:])
	p.elements = p.elements[:base]
	return array, nil
}
