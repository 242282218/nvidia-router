package chat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const maxDecodedImageBytes = 20 << 20

var supportedImageDataPrefixes = map[string]struct{}{
	"data:image/png;base64":  {},
	"data:image/jpeg;base64": {},
	"data:image/webp;base64": {},
	"data:image/gif;base64":  {},
}

type parsedMessage struct {
	valid          bool
	role           jsonString
	roleAlias      bool
	content        messageContent
	contentAlias   bool
	toolCalls      jsonArray
	toolCallsAlias bool
}

type jsonString struct {
	present bool
	valid   bool
	value   string
}

func (s *jsonString) UnmarshalJSON(data []byte) error {
	s.value = ""
	s.present = true
	s.valid = false
	var value *string
	if err := json.Unmarshal(data, &value); err != nil || value == nil {
		return nil
	}
	s.value = *value
	s.valid = true
	return nil
}

type contentKind uint8

const (
	contentNull contentKind = iota
	contentText
	contentParts
)

type messageContent struct {
	present bool
	valid   bool
	kind    contentKind
	parts   []contentPart
}

type jsonArray struct {
	present bool
	valid   bool
	length  int
}

type contentPart struct {
	valid         bool
	partType      jsonString
	typeAlias     bool
	imageURL      imageURLObject
	imageURLAlias bool
}

type imageURLObject struct {
	present  bool
	valid    bool
	url      jsonString
	urlAlias bool
}

type jsonScanner struct {
	data []byte
	pos  int
}

func newJSONScanner(data []byte) *jsonScanner {
	return &jsonScanner{data: data}
}

func (scanner *jsonScanner) more(closing byte) bool {
	scanner.skipWhitespace()
	if scanner.pos < len(scanner.data) && scanner.data[scanner.pos] == ',' {
		scanner.pos++
		scanner.skipWhitespace()
	}
	return scanner.pos < len(scanner.data) && scanner.data[scanner.pos] != closing
}

func (scanner *jsonScanner) skipWhitespace() {
	for scanner.pos < len(scanner.data) {
		switch scanner.data[scanner.pos] {
		case ' ', '\t', '\r', '\n':
			scanner.pos++
		default:
			return
		}
	}
}

func parseMessage(scanner *jsonScanner, index int) (parsedMessage, error) {
	var message parsedMessage
	object, err := consumeJSONOpening(scanner, '{')
	if err != nil || !object {
		return message, err
	}
	message.valid = true
	var seenRole, seenContent, seenToolCalls bool
	for scanner.more('}') {
		key, err := nextJSONObjectKey(scanner)
		if err != nil {
			return message, err
		}
		if err := parseMessageField(scanner, index, key, &message, &seenRole, &seenContent, &seenToolCalls); err != nil {
			return message, err
		}
	}
	return message, consumeJSONClosing(scanner, '}')
}

func parseMessageField(
	scanner *jsonScanner,
	index int,
	key string,
	message *parsedMessage,
	seenRole, seenContent, seenToolCalls *bool,
) error {
	switch key {
	case "role":
		if *seenRole {
			return duplicateJSONField(scanner, fmt.Sprintf("messages[%d].role", index))
		}
		*seenRole = true
		value, err := readJSONString(scanner)
		message.role = value
		return err
	case "content":
		if *seenContent {
			return duplicateJSONField(scanner, fmt.Sprintf("messages[%d].content", index))
		}
		*seenContent = true
		value, err := parseMessageContent(scanner, index)
		message.content = value
		return err
	case "tool_calls":
		if *seenToolCalls {
			return duplicateJSONField(scanner, fmt.Sprintf("messages[%d].tool_calls", index))
		}
		*seenToolCalls = true
		value, err := readJSONArray(scanner)
		message.toolCalls = value
		return err
	default:
		markMessageAlias(message, key)
		return skipJSONValue(scanner)
	}
}

func markMessageAlias(message *parsedMessage, key string) {
	switch {
	case strings.EqualFold(key, "role"):
		message.roleAlias = true
	case strings.EqualFold(key, "content"):
		message.contentAlias = true
	case strings.EqualFold(key, "tool_calls"):
		message.toolCallsAlias = true
	}
}

func parseMessageContent(scanner *jsonScanner, messageIndex int) (messageContent, error) {
	content := messageContent{present: true}
	scanner.skipWhitespace()
	if scanner.pos >= len(scanner.data) {
		return content, fmt.Errorf("decode JSON content: missing value")
	}
	switch scanner.data[scanner.pos] {
	case 'n':
		err := scanner.consumeLiteral("null")
		if err != nil {
			return content, err
		}
		content.valid = true
		content.kind = contentNull
	case '"':
		if err := scanner.skipString(); err != nil {
			return content, err
		}
		content.valid = true
		content.kind = contentText
	case '[':
		if _, err := consumeJSONOpening(scanner, '['); err != nil {
			return content, err
		}
		content.valid = true
		content.kind = contentParts
		parts, err := parseContentParts(scanner, messageIndex)
		content.parts = parts
		return content, err
	default:
		if err := skipJSONValue(scanner); err != nil {
			return content, err
		}
	}
	return content, nil
}

func parseContentParts(scanner *jsonScanner, messageIndex int) ([]contentPart, error) {
	parts := make([]contentPart, 0)
	for scanner.more(']') {
		part, err := parseContentPart(scanner, messageIndex, len(parts))
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, consumeJSONClosing(scanner, ']')
}

func parseContentPart(scanner *jsonScanner, messageIndex, partIndex int) (contentPart, error) {
	var part contentPart
	object, err := consumeJSONOpening(scanner, '{')
	if err != nil || !object {
		return part, err
	}
	part.valid = true
	var seenType, seenImageURL bool
	for scanner.more('}') {
		key, err := nextJSONObjectKey(scanner)
		if err != nil {
			return part, err
		}
		if err := parseContentPartField(scanner, messageIndex, partIndex, key, &part, &seenType, &seenImageURL); err != nil {
			return part, err
		}
	}
	return part, consumeJSONClosing(scanner, '}')
}

func parseContentPartField(
	scanner *jsonScanner,
	messageIndex, partIndex int,
	key string,
	part *contentPart,
	seenType, seenImageURL *bool,
) error {
	param := fmt.Sprintf("messages[%d].content[%d]", messageIndex, partIndex)
	switch key {
	case "type":
		if *seenType {
			return duplicateJSONField(scanner, param+".type")
		}
		*seenType = true
		value, err := readJSONString(scanner)
		part.partType = value
		return err
	case "image_url":
		if *seenImageURL {
			return duplicateJSONField(scanner, param+".image_url")
		}
		*seenImageURL = true
		value, err := parseImageURLObject(scanner, param)
		part.imageURL = value
		return err
	default:
		part.typeAlias = part.typeAlias || strings.EqualFold(key, "type")
		part.imageURLAlias = part.imageURLAlias || strings.EqualFold(key, "image_url")
		return skipJSONValue(scanner)
	}
}

func parseImageURLObject(scanner *jsonScanner, partParam string) (imageURLObject, error) {
	image := imageURLObject{present: true}
	object, err := consumeJSONOpening(scanner, '{')
	if err != nil || !object {
		return image, err
	}
	image.valid = true
	seenURL := false
	for scanner.more('}') {
		key, err := nextJSONObjectKey(scanner)
		if err != nil {
			return image, err
		}
		if key != "url" {
			image.urlAlias = image.urlAlias || strings.EqualFold(key, "url")
			if err := skipJSONValue(scanner); err != nil {
				return image, err
			}
			continue
		}
		if seenURL {
			return image, duplicateJSONField(scanner, partParam+".image_url.url")
		}
		seenURL = true
		image.url, err = readJSONString(scanner)
		if err != nil {
			return image, err
		}
	}
	return image, consumeJSONClosing(scanner, '}')
}

func readJSONString(scanner *jsonScanner) (jsonString, error) {
	value := jsonString{present: true}
	scanner.skipWhitespace()
	if scanner.pos < len(scanner.data) && scanner.data[scanner.pos] == '"' {
		text, err := scanner.readString()
		if err != nil {
			return value, err
		}
		value.valid = true
		value.value = text
		return value, nil
	}
	return value, skipJSONValue(scanner)
}

func readJSONArray(scanner *jsonScanner) (jsonArray, error) {
	array := jsonArray{present: true}
	opened, err := consumeJSONOpening(scanner, '[')
	if err != nil || !opened {
		return array, err
	}
	array.valid = true
	for scanner.more(']') {
		if err := skipJSONValue(scanner); err != nil {
			return array, err
		}
		array.length++
	}
	return array, consumeJSONClosing(scanner, ']')
}

func nextJSONObjectKey(scanner *jsonScanner) (string, error) {
	key, err := scanner.readString()
	if err != nil {
		return "", err
	}
	scanner.skipWhitespace()
	if scanner.pos >= len(scanner.data) || scanner.data[scanner.pos] != ':' {
		return "", fmt.Errorf("decode JSON object key: missing colon")
	}
	scanner.pos++
	return key, nil
}

func consumeJSONOpening(scanner *jsonScanner, expected byte) (bool, error) {
	scanner.skipWhitespace()
	if scanner.pos < len(scanner.data) && scanner.data[scanner.pos] == expected {
		scanner.pos++
		return true, nil
	}
	return false, skipJSONValue(scanner)
}

func consumeJSONClosing(scanner *jsonScanner, expected byte) error {
	scanner.skipWhitespace()
	if scanner.pos >= len(scanner.data) || scanner.data[scanner.pos] != expected {
		return fmt.Errorf("decode JSON closing delimiter: want %c", expected)
	}
	scanner.pos++
	return nil
}

func skipJSONValue(scanner *jsonScanner) error {
	scanner.skipWhitespace()
	if scanner.pos >= len(scanner.data) {
		return fmt.Errorf("decode JSON value: missing value")
	}
	switch scanner.data[scanner.pos] {
	case '{', '[':
		return skipJSONComposite(scanner, scanner.data[scanner.pos])
	case '"':
		return scanner.skipString()
	case 't':
		return scanner.consumeLiteral("true")
	case 'f':
		return scanner.consumeLiteral("false")
	case 'n':
		return scanner.consumeLiteral("null")
	default:
		return scanner.skipNumber()
	}
}

func skipJSONComposite(scanner *jsonScanner, opening byte) error {
	closing := byte(']')
	if opening == '{' {
		closing = '}'
	} else if opening != '[' {
		return fmt.Errorf("decode JSON composite: unexpected delimiter %c", opening)
	}
	scanner.pos++
	for scanner.more(closing) {
		if opening == '{' {
			if _, err := nextJSONObjectKey(scanner); err != nil {
				return err
			}
		}
		if err := skipJSONValue(scanner); err != nil {
			return err
		}
	}
	return consumeJSONClosing(scanner, closing)
}

func duplicateJSONField(scanner *jsonScanner, param string) error {
	if err := skipJSONValue(scanner); err != nil {
		return err
	}
	return invalidRequest("invalid_parameter", param, "Known request fields must not be repeated.")
}

func (scanner *jsonScanner) readString() (string, error) {
	scanner.skipWhitespace()
	if scanner.pos >= len(scanner.data) || scanner.data[scanner.pos] != '"' {
		return "", fmt.Errorf("decode JSON string: expected quote")
	}
	start := scanner.pos + 1
	scanner.pos = start
	hasEscape := false
	for scanner.pos < len(scanner.data) {
		switch scanner.data[scanner.pos] {
		case '\\':
			hasEscape = true
			scanner.pos += 2
		case '"':
			end := scanner.pos
			scanner.pos++
			if !hasEscape {
				return string(scanner.data[start:end]), nil
			}
			var value string
			if err := json.Unmarshal(scanner.data[start-1:scanner.pos], &value); err != nil {
				return "", err
			}
			return value, nil
		default:
			scanner.pos++
		}
	}
	return "", fmt.Errorf("decode JSON string: unterminated string")
}

func (scanner *jsonScanner) skipString() error {
	scanner.skipWhitespace()
	if scanner.pos >= len(scanner.data) || scanner.data[scanner.pos] != '"' {
		return fmt.Errorf("decode JSON string: expected quote")
	}
	scanner.pos++
	for scanner.pos < len(scanner.data) {
		switch scanner.data[scanner.pos] {
		case '\\':
			scanner.pos += 2
		case '"':
			scanner.pos++
			return nil
		default:
			scanner.pos++
		}
	}
	return fmt.Errorf("decode JSON string: unterminated string")
}

func (scanner *jsonScanner) consumeLiteral(literal string) error {
	if len(scanner.data)-scanner.pos < len(literal) ||
		string(scanner.data[scanner.pos:scanner.pos+len(literal)]) != literal {
		return fmt.Errorf("decode JSON literal: want %s", literal)
	}
	scanner.pos += len(literal)
	return nil
}

func (scanner *jsonScanner) skipNumber() error {
	start := scanner.pos
	for scanner.pos < len(scanner.data) {
		switch scanner.data[scanner.pos] {
		case ' ', '\t', '\r', '\n', ',', ']', '}':
			if scanner.pos == start {
				return fmt.Errorf("decode JSON number: missing value")
			}
			return nil
		default:
			scanner.pos++
		}
	}
	if scanner.pos == start {
		return fmt.Errorf("decode JSON number: missing value")
	}
	return nil
}

func validateMessageContent(content messageContent, messageIndex int) (bool, error) {
	if !content.present {
		return false, nil
	}
	if !content.valid {
		param := fmt.Sprintf("messages[%d].content", messageIndex)
		return false, invalidRequest("invalid_parameter", param, "Message content must be text, null, or an array of content parts.")
	}
	if content.kind != contentParts {
		return false, nil
	}
	return validateContentParts(content.parts, messageIndex)
}

func validateContentParts(parts []contentPart, messageIndex int) (bool, error) {
	hasImage := false
	for partIndex, part := range parts {
		partParam := fmt.Sprintf("messages[%d].content[%d]", messageIndex, partIndex)
		if !part.valid {
			return false, invalidRequest("invalid_parameter", partParam, "Each message content part must be an object.")
		}
		if !part.partType.present {
			return false, invalidRequest("missing_required_parameter", partParam+".type", "Each message content part must include a type.")
		}
		if part.typeAlias || !part.partType.valid || part.partType.value == "" {
			return false, invalidRequest("invalid_parameter", partParam+".type", "The content part type must be a non-empty string.")
		}
		if part.partType.value != "image_url" {
			continue
		}
		if part.imageURL.present && part.imageURLAlias {
			return false, invalidRequest("invalid_parameter", partParam+".image_url", "The image_url field is ambiguous.")
		}
		if err := validateImagePart(part.imageURL, partParam); err != nil {
			return false, err
		}
		hasImage = true
	}
	return hasImage, nil
}

func validateImagePart(image imageURLObject, param string) error {
	if !image.present {
		return invalidRequest("missing_required_parameter", param+".image_url", "An image_url content part must include image_url.")
	}
	if !image.valid {
		return invalidRequest("invalid_parameter", param+".image_url", "The image_url parameter must be an object.")
	}
	if !image.url.present {
		return invalidRequest("missing_required_parameter", param+".image_url.url", "An image URL is required.")
	}
	if image.urlAlias || !image.url.valid || image.url.value == "" {
		return invalidRequest("invalid_parameter", param+".image_url.url", "The image URL must be a non-empty string.")
	}
	return validateImageSource(image.url.value, param+".image_url.url")
}

func validateImageSource(source, param string) error {
	if strings.HasPrefix(source, "data:") {
		return validateDataImage(source, param)
	}
	parsed, err := url.ParseRequestURI(source)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return invalidRequest("invalid_image_url", param, "Remote images must use an HTTPS URL without credentials.")
	}
	return nil
}

func validateDataImage(source, param string) error {
	metadata, encoded, found := strings.Cut(source, ",")
	if !found {
		return invalidImageData(param)
	}
	if _, ok := supportedImageDataPrefixes[metadata]; !ok {
		return invalidImageData(param)
	}
	decodedBytes, valid := strictBase64DecodedLength(encoded)
	if !valid || decodedBytes == 0 {
		return invalidImageData(param)
	}
	if decodedBytes > maxDecodedImageBytes {
		return invalidRequest("image_too_large", param, "A decoded image exceeds the 20 MiB limit.")
	}
	decoder := base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(encoded))
	written, err := io.Copy(io.Discard, decoder)
	if err != nil || written != int64(decodedBytes) {
		return invalidImageData(param)
	}
	return nil
}

func strictBase64DecodedLength(encoded string) (int, bool) {
	if encoded == "" || len(encoded)%4 != 0 {
		return 0, false
	}
	padding := 0
	for index := len(encoded) - 1; index >= 0 && encoded[index] == '='; index-- {
		padding++
	}
	if padding > 2 || !validBase64Alphabet(encoded, padding) {
		return 0, false
	}
	return len(encoded)/4*3 - padding, true
}

func validBase64Alphabet(encoded string, padding int) bool {
	dataEnd := len(encoded) - padding
	for index := range encoded {
		character := encoded[index]
		if index >= dataEnd {
			if character != '=' {
				return false
			}
			continue
		}
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' || character == '+' || character == '/' {
			continue
		}
		return false
	}
	return true
}

func invalidImageData(param string) error {
	return invalidRequest("invalid_image_data", param, "Data images must use a supported MIME type and strict Base64 encoding.")
}
