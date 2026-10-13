// Package utils provides type-independent snapshot helpers.
package utils

import "reflect"

// Clone copies acyclic data, including nested metadata, slices, maps and pointers.
// Functions, channels, errors and unexported struct fields retain their original
// values. Preserving errors keeps errors.Is/errors.As and sentinel identity intact.
func Clone[T any](value T) T {
	var result T
	reflect.ValueOf(&result).Elem().Set(clone(reflect.ValueOf(&value).Elem()))
	return result
}

func clone(value reflect.Value) reflect.Value {
	if value.CanInterface() {
		if _, isError := value.Interface().(error); isError {
			return value
		}
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.New(value.Type()).Elem()
		if value.Kind() == reflect.Pointer {
			result.Set(reflect.New(value.Type().Elem()))
			result.Elem().Set(clone(value.Elem()))
		} else {
			result.Set(clone(value.Elem()))
		}
		return result
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			result.SetMapIndex(iterator.Key(), clone(iterator.Value()))
		}
		return result
	case reflect.Slice, reflect.Array:
		var result reflect.Value
		if value.Kind() == reflect.Slice {
			if value.IsNil() {
				return reflect.Zero(value.Type())
			}
			result = reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		} else {
			result = reflect.New(value.Type()).Elem()
		}
		for index := 0; index < value.Len(); index++ {
			result.Index(index).Set(clone(value.Index(index)))
		}
		return result
	case reflect.Struct:
		result := reflect.New(value.Type()).Elem()
		result.Set(value)
		for index := 0; index < value.NumField(); index++ {
			if value.Type().Field(index).IsExported() {
				result.Field(index).Set(clone(value.Field(index)))
			}
		}
		return result
	default:
		return value
	}
}
