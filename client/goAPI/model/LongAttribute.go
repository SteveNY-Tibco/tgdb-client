/**
 * Copyright 2018-19 TIBCO Software Inc. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); You may not use this file except
 * in compliance with the License.
 * A copy of the License is included in the distribution package with this file.
 * You also may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF DirectionAny KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * File name: LongAttribute.go
 * Created on: Oct 13, 2018
 * Created by: achavan
 * SVN id: $id: $
 *
 */

package model

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"github.com/TIBCOSoftware/tgdb-client/client/goAPI/exception"
	"github.com/TIBCOSoftware/tgdb-client/client/goAPI/iostream"
	"github.com/TIBCOSoftware/tgdb-client/client/goAPI/types"
	"reflect"
	"strings"
)

type LongAttribute struct {
	*AbstractAttribute
}

// Create NewTGDecimal Attribute Instance
func DefaultLongAttribute() *LongAttribute {
	// We must register the concrete type for the encoder and decoder (which would
	// normally be on a separate machine from the encoder). On each end, this tells the
	// engine which concrete type is being sent that implements the interface.
	gob.Register(LongAttribute{})

	newAttribute := LongAttribute{
		AbstractAttribute: defaultNewAbstractAttribute(),
	}
	return &newAttribute
}

func NewLongAttributeWithOwner(ownerEntity types.TGEntity) *LongAttribute {
	newAttribute := DefaultLongAttribute()
	newAttribute.owner = ownerEntity
	return newAttribute
}

func NewLongAttribute(attrDesc *AttributeDescriptor) *LongAttribute {
	newAttribute := DefaultLongAttribute()
	newAttribute.attrDesc = attrDesc
	return newAttribute
}

func NewLongAttributeWithDesc(ownerEntity types.TGEntity, attrDesc *AttributeDescriptor, value interface{}) *LongAttribute {
	newAttribute := NewLongAttributeWithOwner(ownerEntity)
	newAttribute.attrDesc = attrDesc
	newAttribute.attrValue = value
	return newAttribute
}

/////////////////////////////////////////////////////////////////
// Helper functions for LongAttribute
/////////////////////////////////////////////////////////////////

func lRound64(val float64) int64 {
	if val < 0 { return int64(val-0.5) }
	return int64(val+0.5)
}

func lRound32(val float32) int64 {
	if val < 0 { return int64(val-0.5) }
	return int64(val+0.5)
}

func (obj *LongAttribute) SetLong(b int64) {
	if !obj.IsNull() && obj.attrValue == b {
		return
	}
	obj.attrValue = b
	obj.setIsModified(true)
}

/////////////////////////////////////////////////////////////////
// Implement functions from Interface ==> TGAttribute
/////////////////////////////////////////////////////////////////

// GetAttributeDescriptor returns the AttributeDescriptor for this attribute
func (obj *LongAttribute) GetAttributeDescriptor() types.TGAttributeDescriptor {
	return obj.getAttributeDescriptor()
}

// GetIsModified checks whether the attribute modified or not
func (obj *LongAttribute) GetIsModified() bool {
	return obj.getIsModified()
}

// GetName gets the name for this attribute as the most generic form
func (obj *LongAttribute) GetName() string {
	return obj.getName()
}

// GetOwner gets owner Entity of this attribute
func (obj *LongAttribute) GetOwner() types.TGEntity {
	return obj.getOwner()
}

// GetValue gets the value for this attribute as the most generic form
func (obj *LongAttribute) GetValue() interface{} {
	return obj.getValue()
}

// IsNull checks whether the attribute value is null or not
func (obj *LongAttribute) IsNull() bool {
	return obj.isNull()
}

// ResetIsModified resets the IsModified flag - recursively, if needed
func (obj *LongAttribute) ResetIsModified() {
	obj.resetIsModified()
}

// SetOwner sets the owner entity - Need this indirection to traverse the chain
func (obj *LongAttribute) SetOwner(ownerEntity types.TGEntity) {
	obj.setOwner(ownerEntity)
}

// SetValue sets the value for this attribute. Appropriate data conversion to its attribute desc will be performed
// If the object is Null, then the object is explicitly set, but no value is provided.
func (obj *LongAttribute) SetValue(value interface{}) types.TGError {
	if value == nil {
		obj.attrValue = value
		obj.setIsModified(true)
		return nil
	}
	if !obj.IsNull() && obj.attrValue == value {
		return nil
	}

	if reflect.TypeOf(value).Kind() != reflect.Int &&
		reflect.TypeOf(value).Kind() != reflect.Int16 &&
		reflect.TypeOf(value).Kind() != reflect.Int32 &&
		reflect.TypeOf(value).Kind() != reflect.Int64 &&
		reflect.TypeOf(value).Kind() != reflect.Float32 &&
		reflect.TypeOf(value).Kind() != reflect.Float64 &&
		reflect.TypeOf(value).Kind() != reflect.String {
		logger.Error(fmt.Sprint("ERROR: Returning LongAttribute:SetValue - attribute value is NOT in expected format/type"))
		errMsg := fmt.Sprintf("Failure to cast the attribute value to LongAttribute")
		return exception.GetErrorByType(types.TGErrorTypeCoercionNotSupported, types.INTERNAL_SERVER_ERROR, errMsg, "")
	}

	if reflect.TypeOf(value).Kind() == reflect.String {
		v, err := StringToLong(value.(string))
		if err != nil {
			logger.Error(fmt.Sprint("ERROR: Returning LongAttribute:SetValue - unable to extract attribute value in string format/type"))
			errMsg := fmt.Sprintf("Failure to covert string to LongAttribute")
			return exception.GetErrorByType(types.TGErrorTypeCoercionNotSupported, types.INTERNAL_SERVER_ERROR, errMsg, err.Error())
		}
		obj.SetLong(v)
	} else if reflect.TypeOf(value).Kind() == reflect.Float32 {
		v := reflect.ValueOf(value).Float()
		obj.SetLong(lRound32(float32(v)))
	} else if reflect.TypeOf(value).Kind() == reflect.Float64 {
		v := reflect.ValueOf(value).Float()
		obj.SetLong(lRound64(float64(v)))
	} else if reflect.TypeOf(value).Kind() != reflect.Int {
		v := reflect.ValueOf(value).Int()
		obj.SetLong(int64(v))
	} else if reflect.TypeOf(value).Kind() != reflect.Int32 {
		v := reflect.ValueOf(value).Int()
		obj.SetLong(int64(v))
	} else if reflect.TypeOf(value).Kind() != reflect.Int16 {
		v := reflect.ValueOf(value).Int()
		obj.SetLong(int64(v))
	} else {
		obj.SetLong(value.(int64))
	}
	return nil
}

// ReadValue reads the value from input stream
func (obj *LongAttribute) ReadValue(is types.TGInputStream) types.TGError {
	if obj.GetAttributeDescriptor().IsEncrypted() {
		err := AbstractAttributeReadDecrypted(obj, is)
		if err != nil {
			logger.Error(fmt.Sprint("ERROR: Returning LongAttribute:ReadValue w/ Error in AbstractAttributeReadDecrypted()"))
			return err
		}
	} else {
		value, err := is.(*iostream.ProtocolDataInputStream).ReadLong()
		if err != nil {
			logger.Error(fmt.Sprint("ERROR: Returning LongAttribute:ReadValue w/ Error in reading value from message buffer"))
			return err
		}
		logger.Log(fmt.Sprintf("Returning LongAttribute::ReadValue - read value: '%+v'", value))
		obj.attrValue = value
	}
	return nil
}

// WriteValue writes the value to output stream
func (obj *LongAttribute) WriteValue(os types.TGOutputStream) types.TGError {
	if obj.GetAttributeDescriptor().IsEncrypted() {
		err := AbstractAttributeWriteEncrypted(obj, os)
		if err != nil {
			logger.Error(fmt.Sprintf("ERROR: Returning LongAttribute:WriteValue - Unable to AbstractAttributeWriteEncrypted() w/ Error: '%s'", err.Error()))
			errMsg := "LongAttribute::WriteValue - Unable to AbstractAttributeWriteEncrypted()"
			return exception.GetErrorByType(types.TGErrorIOException, "TGErrorIOException", errMsg, err.GetErrorDetails())
		}
	} else {
		iValue := reflect.ValueOf(obj.attrValue).Int()
		os.(*iostream.ProtocolDataOutputStream).WriteLong(iValue)
	}
	return nil
}

func (obj *LongAttribute) String() string {
	var buffer bytes.Buffer
	buffer.WriteString("LongAttribute:{")
	strArray := []string{buffer.String(), obj.attributeToString()+"}"}
	msgStr := strings.Join(strArray, ", ")
	return  msgStr
}

/////////////////////////////////////////////////////////////////
// Implement functions from Interface ==> types.TGSerializable
/////////////////////////////////////////////////////////////////

// ReadExternal reads the byte format from an external input stream and constructs a system object
func (obj *LongAttribute) ReadExternal(is types.TGInputStream) types.TGError {
	return AbstractAttributeReadExternal(obj, is)
}

// WriteExternal writes a system object into an appropriate byte format onto an external output stream
func (obj *LongAttribute) WriteExternal(os types.TGOutputStream) types.TGError {
	return AbstractAttributeWriteExternal(obj, os)
}

/////////////////////////////////////////////////////////////////
// Implement functions from Interface ==> encoding/BinaryMarshaller
/////////////////////////////////////////////////////////////////

func (obj *LongAttribute) MarshalBinary() ([]byte, error) {
	// A simple encoding: plain text.
	var b bytes.Buffer
	_, err := fmt.Fprintln(&b, obj.owner, obj.attrDesc, obj.attrValue, obj.isModified)
	if err != nil {
		logger.Error(fmt.Sprintf("ERROR: Returning LongAttribute:MarshalBinary w/ Error: '%+v'", err.Error()))
		return nil, err
	}
	return b.Bytes(), nil
}

/////////////////////////////////////////////////////////////////
// Implement functions from Interface ==> encoding/BinaryUnmarshaller
/////////////////////////////////////////////////////////////////

func (obj *LongAttribute) UnmarshalBinary(data []byte) error {
	// A simple encoding: plain text.
	b := bytes.NewBuffer(data)
	_, err := fmt.Fscanln(b, &obj.owner, &obj.attrDesc, &obj.attrValue, &obj.isModified)
	if err != nil {
		logger.Error(fmt.Sprintf("ERROR: Returning LongAttribute:UnmarshalBinary w/ Error: '%+v'", err.Error()))
		return err
	}
	return err
}
