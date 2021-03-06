package dsc

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/toolbox"
)

type metaRecordMapper struct {
	converter        toolbox.Converter
	structType       interface{}
	columnToFieldMap map[string](map[string]string)
	usePointer       bool
}

//NewMetaRecordMapped creates a new MetaRecordMapped to map a data record to a struct, it takes target struct and flag if it is a pointer as parameters.
func NewMetaRecordMapped(targetType interface{}, usePointer bool) RecordMapper {
	structType := targetType
	if usePointer {
		var originalType = targetType.(reflect.Type).Elem()
		structType = originalType
	}
	return &metaRecordMapper{
		converter:        *toolbox.NewColumnConverter(""),
		structType:       structType,
		usePointer:       usePointer,
		columnToFieldMap: toolbox.NewFieldSettingByKey(targetType, "column")}
}

func (rm *metaRecordMapper) getValueMappingCount(columns []string) int {
	result := 0
	for _, key := range columns {
		if mapping, ok := rm.columnToFieldMap[strings.ToLower(key)]; ok {
			if _, found := mapping["valueMap"]; found {
				result++
			}
		}
	}

	return result
}

func (rm *metaRecordMapper) allocateValueMapByKey(columns []string) map[string]interface{} {
	var valuesPointers = make([]interface{}, rm.getValueMappingCount(columns))
	index := 0
	var result = make(map[string]interface{})
	for _, key := range columns {
		if mapping, ok := rm.columnToFieldMap[strings.ToLower(key)]; ok {
			if _, found := mapping["valueMap"]; found {
				result[key] = &valuesPointers[index]
				index++
			}
		}
	}
	return result
}

func (rm *metaRecordMapper) applyFieldMapValuesIfNeeded(fieldsValueMap map[string]interface{}, structPointer reflect.Value) error {
	for key, rawValue := range fieldsValueMap {

		valueMapping := rm.columnToFieldMap[strings.ToLower(key)]
		fieldName := valueMapping["fieldName"]
		field := structPointer.Elem().FieldByName(fieldName)
		unwrappedValue := reflect.ValueOf(rawValue).Elem()
		if unwrappedValue.IsNil() {
			if field.Kind() != reflect.Ptr {
				return fmt.Errorf("Failed to apply value map on %v, unable to set nil", fieldName)
			}
			continue
		}

		rawValue = unwrappedValue.Interface()
		var value string
		if valueAsBytes, ok := rawValue.([]byte); ok {
			value = string(valueAsBytes)
		} else {
			value = toolbox.AsString(rawValue)
		}

		valueMap := toolbox.MakeStringMap(valueMapping["valueMap"], ":", ",")
		stringValue := toolbox.AsString(value)
		if mappedValue, found := valueMap[stringValue]; found {
			fieldValuePointer := field.Addr().Interface()
			err := rm.converter.AssignConverted(fieldValuePointer, mappedValue)
			if err != nil {
				return fmt.Errorf("Failed to map record, unable convert,dur to %v", err)

			}
		} else {
			return fmt.Errorf("Failed to map record, unable to find valid mapping, want one of %s, but had %v", valueMap, stringValue)
		}

	}
	return nil
}

func (rm *metaRecordMapper) scanData(scanner Scanner) (result interface{}, err error) {
	structType := toolbox.DiscoverTypeByKind(rm.structType, reflect.Struct)
	structPointer := reflect.New(structType)
	resultStruct := structPointer.Elem()
	columns, _ := scanner.Columns()
	var fieldValuePointers = make([]interface{}, len(columns))

	var fieldsValueMap map[string]interface{}

	hasFieldValueMap := rm.getValueMappingCount(columns) > 0
	if hasFieldValueMap {
		fieldsValueMap = rm.allocateValueMapByKey(columns)
	}
	for i, key := range columns {

		if fieldMapping, ok := rm.columnToFieldMap[strings.ToLower(key)]; ok {
			fieldName := fieldMapping["fieldName"]
			field := resultStruct.FieldByName(fieldName)

			if _, found := fieldMapping["valueMap"]; found {
				fieldValuePointers[i] = fieldsValueMap[key]
				continue
			}

			fieldValuePointers[i] = field.Addr().Interface()

		} else {
			return nil, fmt.Errorf("Unable map column %v to the result", key)
		}
	}
	err = scanner.Scan(fieldValuePointers...)
	if err != nil {
		return nil, err
	}

	if hasFieldValueMap {
		err := rm.applyFieldMapValuesIfNeeded(fieldsValueMap, structPointer)
		if err != nil {
			return nil, err
		}
	}

	if !rm.usePointer {
		result = structPointer.Elem().Interface()
		return result, err
	}
	return structPointer.Interface(), err
}

func (rm *metaRecordMapper) mapFromValues(vaues []interface{}) (result interface{}, err error) {
	return nil, nil
}

func (rm *metaRecordMapper) Map(scanner Scanner) (result interface{}, err error) {
	return rm.scanData(scanner)
}

type columnarRecordMapper struct {
	usePointer  bool
	targetSlice reflect.Type
}

//NewColumnarRecordMapper creates a new ColumnarRecordMapper, to map a data record to slice.
func NewColumnarRecordMapper(usePointer bool, targetSlice reflect.Type) RecordMapper {
	return &columnarRecordMapper{usePointer, targetSlice}
}

func (rm *columnarRecordMapper) Map(scanner Scanner) (interface{}, error) {
	result, _, err := ScanRow(scanner)
	if err != nil {
		return nil, err
	}
	if rm.usePointer {
		return result, nil
	}
	return result, nil
}

//NewRecordMapper create a new record mapper, if struct is passed it would be MetaRecordMapper, or for slice ColumnRecordMapper
func NewRecordMapper(targetType reflect.Type) RecordMapper {
	switch targetType.Kind() {
	case reflect.Struct:
		var mapper = NewMetaRecordMapped(targetType, false)
		return mapper
	case reflect.Slice:
		var mapper = NewColumnarRecordMapper(false, targetType)
		return mapper
	case reflect.Ptr:
		if targetType.Elem().Kind() == reflect.Slice {
			var mapper = NewColumnarRecordMapper(true, targetType.Elem())
			return mapper
		} else if targetType.Elem().Kind() == reflect.Struct {
			var mapper = NewMetaRecordMapped(targetType, true)
			return mapper
		}
	default:
		panic("Unsupported type: " + targetType.Name())
	}
	return nil
}

//NewRecordMapperIfNeeded create a new mapper if passed in mapper is nil. It takes target type for the record mapper.
func NewRecordMapperIfNeeded(mapper RecordMapper, targetType reflect.Type) RecordMapper {
	if mapper != nil {
		return mapper
	}
	return NewRecordMapper(targetType)
}

//ScanRow takes scanner to scans row.
func ScanRow(scanner Scanner) ([]interface{}, []string, error) {
	columns, _ := scanner.Columns()

	count := len(columns)
	var valuePointers = make([]interface{}, count)
	var rowValues = make([]interface{}, count)

	for i := range rowValues {
		valuePointers[i] = &rowValues[i]
	}

	err := scanner.Scan(valuePointers...)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to scan row due to %v", err)
	}

	for i := range rowValues {
		var value interface{}
		rawValue := rowValues[i]
		b, ok := rawValue.([]byte)
		if ok {
			value = string(b)
		} else {
			value = rawValue
		}
		rowValues[i] = value
	}
	return rowValues, columns, nil
}
