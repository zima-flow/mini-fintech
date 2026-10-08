package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type Validator interface {
	Validate() error
}

func Load[T any](getenv func(string) string) (T, error) {
	var out T

	v := reflect.ValueOf(&out).Elem()
	if v.Kind() != reflect.Struct {
		return out, fmt.Errorf("config: Load requires a struct, got %s", v.Kind())
	}

	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		name, optional := parseFieldTag(field.Tag.Get("field"))
		if name == "" {
			continue
		}
		raw := getenv(name)
		if raw == "" {
			if optional {
				continue
			}
			return out, fmt.Errorf("config: required variable %s is not set", name)
		}
		if err := decode(v.Field(i), raw); err != nil {
			return out, fmt.Errorf("config: %s: %w", name, err)
		}
	}

	if validator, ok := any(&out).(Validator); ok {
		if err := validator.Validate(); err != nil {
			return out, fmt.Errorf("config: invalid configuration: %w", err)
		}
	}
	return out, nil
}

func parseFieldTag(tag string) (name string, optional bool) {
	if tag == "" {
		return "", false
	}
	name, rest, _ := strings.Cut(tag, ",")
	for rest != "" {
		var opt string
		opt, rest, _ = strings.Cut(rest, ",")
		if opt == "optional" {
			optional = true
		}
	}
	return name, optional
}

func decode(field reflect.Value, raw string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if field.Type() == reflect.TypeOf(time.Duration(0)) {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("invalid duration %q: %w", raw, err)
			}
			field.SetInt(int64(d))
			return nil
		}
		n, err := strconv.ParseInt(raw, 10, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid integer %q: %w", raw, err)
		}
		field.SetInt(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("invalid bool %q: %w", raw, err)
		}
		field.SetBool(b)
	default:
		return fmt.Errorf("unsupported kind %s", field.Kind())
	}
	return nil
}
