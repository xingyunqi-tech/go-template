package entity

// Detail is one field of a NestError.
type Detail struct {
	Name  string
	Value any
}

// NestError is the nestjs HttpException shape returned by the WeChat, Alipay and SMS clients:
// Message is a custom message, ErrorKey the "error" field and Fields the "errors" object.
type NestError struct {
	Status   int
	Message  string
	ErrorKey string
	Fields   []Detail
}

func (e *NestError) Error() string {
	switch {
	case e.Message != "":
		return e.Message
	case e.ErrorKey != "":
		return e.ErrorKey
	case len(e.Fields) > 0:
		if s, ok := e.Fields[0].Value.(string); ok {
			return e.Fields[0].Name + ": " + s
		}

		return e.Fields[0].Name
	default:
		return "http error"
	}
}

// StatusUnprocessable is the status of a NestError raised for invalid input.
const StatusUnprocessable = 422

// NestInvalid is a 422 NestError {field: key}.
func NestInvalid(field, key string) *NestError {
	return NestInvalidFields(Detail{Name: field, Value: key})
}

// NestInvalidFields is a 422 NestError with several fields.
func NestInvalidFields(fields ...Detail) *NestError {
	return &NestError{Status: StatusUnprocessable, Message: "", ErrorKey: "", Fields: fields}
}
