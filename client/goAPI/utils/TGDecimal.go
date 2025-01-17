/**
 * Copyright 2018-19 TIBCO Software Inc. All rights reserved.
 *
 * Portions Copyright (c) 1994 - 2014, PostgreSQL Global Development Group. Version 9.3.3
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
 * File name: TGDecimal.go
 * Created on: Feb 13, 2019
 * Created by: achavan
 * SVN id: $id: $
 *
 */

package utils

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

/**
 * TODO: Revisit later - for more testing and optimization
 * This is an effort to implement a number into internalDecimal format that can be stored and retrieved
 * without any significant rounding errors, that are observed in the usage of GO language implementation
 * of big.Float and/or big.Int.
 *
 * Some of the references:
 * https://ieeexplore.ieee.org/document/4610935/metrics#metrics
 * https://www.wikihow.com/Convert-a-Number-from-Decimal-to-IEEE-754-Floating-Point-Representation
 * https://steve.hollasch.net/cgindex/coding/ieeefloat.html
 * https://www.exploringbinary.com/decimal-precision-of-binary-floating-point-numbers/
 * https://github.com/golang/go/issues/26285
 *
 */

// NOTE: This can represent numbers limited to a maximum of 2^31 digits after the internalDecimal point.
type internalDecimal struct {
	digits     [800]byte // digits, big-endian representation
	usedDigits int       // number of digits used
	decPoint   int       // internalDecimal point
	negFlag    bool      // negative flag
	truncInd   bool      // discarded nonzero digits beyond digits[:usedDigits]
}

// String helps debugging by converting structure into a single string
func (a *internalDecimal) String() string {
	n := 10 + a.usedDigits
	if a.decPoint > 0 {
		n += a.decPoint
	}
	if a.decPoint < 0 {
		n += -a.decPoint
	}

	buf := make([]byte, n)
	bufLen := 0
	switch {
	case a.usedDigits == 0:
		return "0"
	case a.decPoint <= 0:
		// fill space between internalDecimal point and digits w/ zeros
		buf[bufLen] = '0'
		bufLen++
		buf[bufLen] = '.'
		bufLen++
		bufLen += digitZero(buf[bufLen : bufLen+-a.decPoint])
		bufLen += copy(buf[bufLen:], a.digits[0:a.usedDigits])
	case a.decPoint < a.usedDigits:
		// internalDecimal point in middle of digits
		bufLen += copy(buf[bufLen:], a.digits[0:a.decPoint])
		buf[bufLen] = '.'
		bufLen++
		bufLen += copy(buf[bufLen:], a.digits[a.decPoint:a.usedDigits])
	default:
		// fill space between digits and internalDecimal point w/ zeros
		bufLen += copy(buf[bufLen:], a.digits[0:a.usedDigits])
		bufLen += digitZero(buf[bufLen : bufLen+a.decPoint-a.usedDigits])
	}
	return string(buf[0:bufLen])
}

func digitZero(dst []byte) int {
	for i := range dst {
		dst[i] = '0'
	}
	return len(dst)
}

// trim trailing zeros from number.
// (They are meaningless; the internalDecimal point is tracked
// independent of the number of digits.)
func trim(d *internalDecimal) {
	for d.usedDigits > 0 && d.digits[d.usedDigits-1] == '0' {
		d.usedDigits--
	}
	if d.usedDigits == 0 {
		d.decPoint = 0
	}
}

// Assign value to d.
func (d *internalDecimal) Assign(value uint64) {
	var buf [24]byte

	// Write reversed internalDecimal in buf.
	n := 0
	for value > 0 {
		v1 := value / 10
		value -= 10 * v1
		buf[n] = byte(value + '0')
		n++
		value = v1
	}

	// Reverse again to produce forward internalDecimal in a.digits.
	d.usedDigits = 0
	for n--; n >= 0; n-- {
		d.digits[d.usedDigits] = buf[n]
		d.usedDigits++
	}
	d.decPoint = d.usedDigits
	trim(d)
}

// Maximum shift that we can do in one pass without overflow.
// A uint has 32 or 64 bits, and we have to be able to accommodate 9<<k.
const uintSize = 32 << (^uint(0) >> 63)
const maxShift = uintSize - 4

// Binary shift right (/ 2) by k bits.  k <= maxShift to avoid overflow.
func rightShift(d *internalDecimal, k uint) {
	r := 0 // read position
	w := 0 // write position

	// Pick up enough leading digits to cover first shift.
	var n uint
	for ; n>>k == 0; r++ {
		if r >= d.usedDigits {
			if n == 0 {
				// a == 0; shouldn't get here, but handle anyway.
				d.usedDigits = 0
				return
			}
			for n>>k == 0 {
				n = n * 10
				r++
			}
			break
		}
		c := uint(d.digits[r])
		n = n*10 + c - '0'
	}
	d.decPoint -= r - 1

	var mask uint = (1 << k) - 1

	// Pick up a digit, put down a digit.
	for ; r < d.usedDigits; r++ {
		c := uint(d.digits[r])
		dig := n >> k
		n &= mask
		d.digits[w] = byte(dig + '0')
		w++
		n = n*10 + c - '0'
	}

	// Put down extra digits.
	for n > 0 {
		dig := n >> k
		n &= mask
		if w < len(d.digits) {
			d.digits[w] = byte(dig + '0')
			w++
		} else if dig > 0 {
			d.truncInd = true
		}
		n = n * 10
	}

	d.usedDigits = w
	trim(d)
}

// TODO: Revisit later - for additional thorough testing and more optimization instead of this raw/crude approach
//
// For example, leftShiftLookup[4] = {2, "625"} means, if we are shifting by 4 (equivalent to multiplying by 2^4=16),
// it will require addition 2 digits when the string prefix is "625" through "999", and
// only 1 additional digit when the string prefix is "000" through "624".
//
// It is a lookup table that helps figure out how many additional digits are needed while shifting left

type leftShiftHelper struct {
	additionalDigits int    // number of new digits
	limit            string // minus one digit if original < a.
}

var leftShiftLookup = []leftShiftHelper{
	// 1/2 ==> 0.5, So leaving aside decimal point, it is always a factorial of 5
	// Leading digits of 1/2^i = 5^i.
	// 5^23 is not an exact 64-bit floating point number,
	// so have to use bc for the math.
	// Go up to 60 to be large enough for 32bit and 64bit platforms.
	/*
		seq 60 | sed 's/^/5^/' | bc |
		awk 'BEGIN{ print "\t{ 0, \"\" }," }
		{
			log2 = log(2)/log(10)
			printf("\t{ %digits, \"%s\" },\t// * %digits\n",
				int(log2*NR+1), $0, 2**NR)
		}'
	*/
	{0, ""},                                            // Multiply by 2^0 = 1
	{1, "5"},                                           // Multiply by 2^1 = 2
	{1, "25"},                                          // Multiply by 2^2 = 4
	{1, "125"},                                         // Multiply by 2^3 = 8
	{2, "625"},                                         // Multiply by 2^4 = 16
	{2, "3125"},                                        // Multiply by 2^5 = 32
	{2, "15625"},                                       // Multiply by 2^6 = 64
	{3, "78125"},                                       // Multiply by 2^7 = 128
	{3, "390625"},                                      // Multiply by 2^8 = 256
	{3, "1953125"},                                     // Multiply by 2^9 = 512
	{4, "9765625"},                                     // Multiply by 2^10 = 1024
	{4, "48828125"},                                    // Multiply by 2^11 = 2048
	{4, "244140625"},                                   // Multiply by 2^12 = 4096
	{4, "1220703125"},                                  // Multiply by 2^13 = 8192
	{5, "6103515625"},                                  // Multiply by 2^14 = 16384
	{5, "30517578125"},                                 // Multiply by 2^15 = 32768
	{5, "152587890625"},                                // Multiply by 2^16 = 65536
	{6, "762939453125"},                                // Multiply by 2^17 = 131072
	{6, "3814697265625"},                               // Multiply by 2^18 = 262144
	{6, "19073486328125"},                              // Multiply by 2^19 = 524288
	{7, "95367431640625"},                              // Multiply by 2^20 = 1048576
	{7, "476837158203125"},                             // Multiply by 2^21 = 2097152
	{7, "2384185791015625"},                            // Multiply by 2^22 = 4194304
	{7, "11920928955078125"},                           // Multiply by 2^23 = 8388608
	{8, "59604644775390625"},                           // Multiply by 2^24 = 16777216
	{8, "298023223876953125"},                          // Multiply by 2^25 = 33554432
	{8, "1490116119384765625"},                         // Multiply by 2^26 = 67108864
	{9, "7450580596923828125"},                         // Multiply by 2^27 = 134217728
	{9, "37252902984619140625"},                        // Multiply by 2^28 = 268435456
	{9, "186264514923095703125"},                       // Multiply by 2^29 = 536870912
	{10, "931322574615478515625"},                      // Multiply by 2^30 = 1073741824
	{10, "4656612873077392578125"},                     // Multiply by 2^31 = 2147483648
	{10, "23283064365386962890625"},                    // Multiply by 2^32 = 4294967296
	{10, "116415321826934814453125"},                   // Multiply by 2^33 = 8589934592
	{11, "582076609134674072265625"},                   // Multiply by 2^34 = 17179869184
	{11, "2910383045673370361328125"},                  // Multiply by 2^35 = 34359738368
	{11, "14551915228366851806640625"},                 // Multiply by 2^36 = 68719476736
	{12, "72759576141834259033203125"},                 // Multiply by 2^37 = 137438953472
	{12, "363797880709171295166015625"},                // Multiply by 2^38 = 274877906944
	{12, "1818989403545856475830078125"},               // Multiply by 2^39 = 549755813888
	{13, "9094947017729282379150390625"},               // Multiply by 2^40 = 1099511627776
	{13, "45474735088646411895751953125"},              // Multiply by 2^41 = 2199023255552
	{13, "227373675443232059478759765625"},             // Multiply by 2^42 = 4398046511104
	{13, "1136868377216160297393798828125"},            // Multiply by 2^43 = 8796093022208
	{14, "5684341886080801486968994140625"},            // Multiply by 2^44 = 17592186044416
	{14, "28421709430404007434844970703125"},           // Multiply by 2^45 = 35184372088832
	{14, "142108547152020037174224853515625"},          // Multiply by 2^46 = 70368744177664
	{15, "710542735760100185871124267578125"},          // Multiply by 2^46 = 140737488355328
	{15, "3552713678800500929355621337890625"},         // Multiply by 2^47 = 281474976710656
	{15, "17763568394002504646778106689453125"},        // Multiply by 2^48 = 562949953421312
	{16, "88817841970012523233890533447265625"},        // Multiply by 2^49 = 1125899906842624
	{16, "444089209850062616169452667236328125"},       // Multiply by 2^50 = 2251799813685248
	{16, "2220446049250313080847263336181640625"},      // Multiply by 2^51 = 4503599627370496
	{16, "11102230246251565404236316680908203125"},     // Multiply by 2^52 = 9007199254740992
	{17, "55511151231257827021181583404541015625"},     // Multiply by 2^53 = 18014398509481984
	{17, "277555756156289135105907917022705078125"},    // Multiply by 2^54 = 36028797018963968
	{17, "1387778780781445675529539585113525390625"},   // Multiply by 2^55 = 72057594037927936
	{18, "6938893903907228377647697925567626953125"},   // Multiply by 2^56 = 144115188075855872
	{18, "34694469519536141888238489627838134765625"},  // Multiply by 2^57 = 288230376151711744
	{18, "173472347597680709441192448139190673828125"}, // Multiply by 2^58 = 576460752303423488
	{19, "867361737988403547205962240695953369140625"}, // Multiply by 2^59 = 1152921504606846976
}

// Is the leading prefix of b lexicographically less than s?
func prefixIsLessThan(b []byte, s string) bool {
	for i := 0; i < len(s); i++ {
		if i >= len(b) {
			return true
		}
		if b[i] != s[i] {
			return b[i] < s[i]
		}
	}
	return false
}

// Binary shift left (* 2) by shiftPos bits.  shiftPos <= maxShift to avoid overflow.
func leftShift(d *internalDecimal, shiftPos uint) {
	delta := leftShiftLookup[shiftPos].additionalDigits
	if prefixIsLessThan(d.digits[0:d.usedDigits], leftShiftLookup[shiftPos].limit) {
		delta--
	}

	r := d.usedDigits         // read position
	w := d.usedDigits + delta // write position

	// Pick up a digit, put down a digit.
	var n uint
	for r--; r >= 0; r-- {
		n += (uint(d.digits[r]) - '0') << shiftPos
		quo := n / 10
		rem := n - 10*quo
		w--
		if w < len(d.digits) {
			d.digits[w] = byte(rem + '0')
		} else if rem != 0 {
			d.truncInd = true
		}
		n = quo
	}

	// Put down extra digits.
	for n > 0 {
		quo := n / 10
		rem := n - 10*quo
		w--
		if w < len(d.digits) {
			d.digits[w] = byte(rem + '0')
		} else if rem != 0 {
			d.truncInd = true
		}
		n = quo
	}

	d.usedDigits += delta
	if d.usedDigits >= len(d.digits) {
		d.usedDigits = len(d.digits)
	}
	d.decPoint += delta
	trim(d)
}

// Delete this once leftShift is implemented and tested using correct logic
func leftShiftFaulty(d *internalDecimal, shiftPos uint) {
	r := d.usedDigits   			  // read position
	w := d.usedDigits + int(shiftPos) // write position

	// Pick up a digit, put down a digit.
	var n uint
	for r--; r >= 0; r-- {
		n += (uint(d.digits[r]) - '0') << shiftPos
		quo := n / 10
		rem := n - 10*quo
		w--
		if w < len(d.digits) {
			d.digits[w] = byte(rem + '0')
		} else if rem != 0 {
			d.truncInd = true
		}
		n = quo
	}

	// Put down extra digits.
	for n > 0 {
		quo := n / 10
		rem := n - 10*quo
		w--
		if w < len(d.digits) {
			d.digits[w] = byte(rem + '0')
		} else if rem != 0 {
			d.truncInd = true
		}
		n = quo
	}

	d.usedDigits += int(shiftPos-n)
	if d.usedDigits >= len(d.digits) {
		d.usedDigits = len(d.digits)
	}
	d.decPoint += int(shiftPos-n)
	trim(d)
}

// Binary shift left (shiftPos > 0) or right (shiftPos < 0).
func (d *internalDecimal) Shift(shiftPos int) {
	switch {
	case d.usedDigits == 0:
		// nothing to do: a == 0
	case shiftPos > 0:
		for shiftPos > maxShift {
			leftShift(d, maxShift)
			shiftPos -= maxShift
		}
		leftShift(d, uint(shiftPos))
	case shiftPos < 0:
		for shiftPos < -maxShift {
			rightShift(d, maxShift)
			shiftPos += maxShift
		}
		rightShift(d, uint(-shiftPos))
	}
}

// If we chop a at usedDigits digits, should we round up?
func shouldRoundUp(d *internalDecimal, noOfDigits int) bool {
	if noOfDigits < 0 || noOfDigits >= d.usedDigits {
		return false
	}
	if d.digits[noOfDigits] == '5' && noOfDigits+1 == d.usedDigits { // exactly halfway - round to even
		// if we truncated, a little higher than what's recorded - always round up
		if d.truncInd {
			return true
		}
		return noOfDigits > 0 && (d.digits[noOfDigits-1]-'0')%2 != 0
	}
	// not halfway - digit tells all
	return d.digits[noOfDigits] >= '5'
}

// Round d to usedDigits digits (or fewer). If usedDigits is zero, it means we're rounding
// just to the left of the digits, as in 0.09 -> 0.1.
func (d *internalDecimal) Round(noOfDigits int) {
	if noOfDigits < 0 || noOfDigits >= d.usedDigits {
		return
	}
	if shouldRoundUp(d, noOfDigits) {
		d.RoundUp(noOfDigits)
	} else {
		d.RoundDown(noOfDigits)
	}
}

// Round d down to usedDigits digits (or fewer).
func (d *internalDecimal) RoundDown(noOfDigits int) {
	if noOfDigits < 0 || noOfDigits >= d.usedDigits {
		return
	}
	d.usedDigits = noOfDigits
	trim(d)
}

// Round d up to usedDigits digits (or fewer).
func (d *internalDecimal) RoundUp(noOfDigits int) {
	if noOfDigits < 0 || noOfDigits >= d.usedDigits {
		return
	}

	// round up
	for i := noOfDigits - 1; i >= 0; i-- {
		c := d.digits[i]
		if c < '9' { // can stop after this digit
			d.digits[i]++
			d.usedDigits = i + 1
			return
		}
	}

	// Change to single 1 with adjusted internalDecimal point.
	d.digits[0] = '1'
	d.usedDigits = 1
	d.decPoint++
}

type ieee754 struct {
	mantissa uint
	exponent uint
	expBias  int
}

var float32info = ieee754{23, 8, -127}
var float64info = ieee754{52, 11, -1023}

// roundShortest rounds digits (= mantissa * 2^exp) to the shortest number of digits
// that will let the original floating point value be precisely reconstructed.
func roundShortest(d *internalDecimal, mant uint64, exp int, flt *ieee754) {
	// If mantissa is zero, the number is zero; stop now.
	if mant == 0 {
		d.usedDigits = 0
		return
	}

	// Compute upper and lower such that any internalDecimal number between upper and lower (possibly inclusive)
	// will round to the original floating point number.
	minexp := flt.expBias + 1 // minimum possible exponent
	if exp > minexp && 332*(d.decPoint-d.usedDigits) >= 100*(exp-int(flt.mantissa)) {
		// The number is already shortest.
		return
	}

	// Next highest floating point number is mant+1 << exp-mantissa.
	// Our upper bound is halfway between, mant*2+1 << exp-mantissa-1.
	upper := new(internalDecimal)
	upper.Assign(mant*2 + 1)
	upper.Shift(exp - int(flt.mantissa) - 1)

	// Next lowest floating point number is mant-1 << exp-mantissa,
	// unless mant-1 drops the significant bit and exp is not the minimum exp,
	// in which case the next lowest is mant*2-1 << exp-mantissa-1.
	// Our lower bound is halfway between, mantlo*2+1 << explo-mantissa-1.
	var mantlo uint64
	var explo int
	if mant > 1<<flt.mantissa || exp == minexp {
		mantlo = mant - 1
		explo = exp
	} else {
		mantlo = mant*2 - 1
		explo = exp - 1
	}
	lower := new(internalDecimal)
	lower.Assign(mantlo*2 + 1)
	lower.Shift(explo - int(flt.mantissa) - 1)

	// Check if the original mantissa is even, In that case, the upper and lower bounds are possible outputs
	// such that IEEE round-to-even would round to the original mantissa and not the neighbors.
	inclusive := mant%2 == 0

	// Let's find out the minimum number of digits required.
	// Walk along until digits has distinguished itself from upper and lower.
	for i := 0; i < d.usedDigits; i++ {
		l := byte('0') // lower digit
		if i < lower.usedDigits {
			l = lower.digits[i]
		}
		m := d.digits[i] // middle digit
		u := byte('0')   // upper digit
		if i < upper.usedDigits {
			u = upper.digits[i]
		}

		// Check whether it is ok to truncate, if lower has a different digit or if it is inclusive and
		// is exactly the result of rounding down (i.e., and we have reached the final digit of lower).
		okDown := l != m || inclusive && i+1 == lower.usedDigits

		// Check whether it is ok to round up if upper has a different digit and whether the upper
		// is either inclusive or bigger than the result of rounding up.
		okUp := m != u && (inclusive || m+1 < u || i+1 < upper.usedDigits)

		// Check if it is ok to do one or other, then round to the nearest one.
		// If there is only one way to proceed, do it.
		switch {
		case okDown && okUp:
			d.Round(i + 1)
			return
		case okDown:
			d.RoundDown(i + 1)
			return
		case okUp:
			d.RoundUp(i + 1)
			return
		}
	}
}

// TODO: Revisit later - for more testing and accuracy
// DivisionPrecision is the number of internalDecimal places in the result when it doesn't divide exactly.
var DivisionPrecision = 16

// MarshalJSONWithoutQuotes should be set to true if the internalDecimal needs to be JSON marshaled as a number,
// instead of as a string.
// WARNING: This may not be safe and accurate as there is a danger of losing precision
var MarshalJSONWithoutQuotes = false

// Zero constant, to make computations faster.
var Zero = NewTGDecimal(0, 1)

var zeroInt = big.NewInt(0)
var oneInt = big.NewInt(1)

//var twoInt = big.NewInt(2)
//var fourInt = big.NewInt(4)
var fiveInt = big.NewInt(5)
var tenInt = big.NewInt(10)

//var twentyInt = big.NewInt(20)

// TGDecimal represents a fixed-point internalDecimal. It is immutable.
// decimalNumber = value * 10 ^ exp
type TGDecimal struct {
	value *big.Int
	exp   int32
}

// NewTGDecimal returns a new fixed-point internalDecimal, value * 10 ^ exp.
func NewTGDecimal(value int64, exp int32) TGDecimal {
	return TGDecimal{
		value: big.NewInt(value),
		exp:   exp,
	}
}

// NewTGDecimalFromBigInt returns a new TGDecimal from a big.Int, value * 10 ^ exp
func NewTGDecimalFromBigInt(value *big.Int, exp int32) TGDecimal {
	return TGDecimal{
		value: big.NewInt(0).Set(value),
		exp:   exp,
	}
}

// NewTGDecimalFromString returns a new TGDecimal from a string representation.
func NewTGDecimalFromString(value string) (TGDecimal, error) {
	originalInput := value
	var intString string
	var exp int64
	emptyDecimal := TGDecimal{}

	// Check if number is using scientific notation
	eIndex := strings.IndexAny(value, "Ee")
	if eIndex != -1 {
		expInt, err := strconv.ParseInt(value[eIndex+1:], 10, 32)
		if err != nil {
			if e, ok := err.(*strconv.NumError); ok && e.Err == strconv.ErrRange {
				return emptyDecimal, fmt.Errorf("unable to convert '%s' to internalDecimal as the fractional part seems too long", value)
			}
			return emptyDecimal, fmt.Errorf("unable to convert '%s' to internalDecimal as the exponent is not numeric", value)
		}
		value = value[:eIndex]
		exp = expInt
	}

	parts := strings.Split(value, ".")
	if len(parts) == 1 {
		// There is no internalDecimal point, we can just parse the original string as an int
		intString = value
	} else if len(parts) == 2 {
		// Trim the insignificant digits for more accurate comparisons.
		decimalPart := strings.TrimRight(parts[1], "0")
		intString = parts[0] + decimalPart
		expInt := -len(decimalPart)
		exp += int64(expInt)
	} else {
		return emptyDecimal, fmt.Errorf("unable to convert '%s' to internalDecimal as it has too many decimal points", value)
	}

	dValue := new(big.Int)
	_, ok := dValue.SetString(intString, 10)
	if !ok {
		return emptyDecimal, fmt.Errorf("unable to convert '%s' to internalDecimal", value)
	}

	if exp < math.MinInt32 || exp > math.MaxInt32 {
		return emptyDecimal, fmt.Errorf("unable to convert '%s' to internalDecimal as the fractional part seems too long", originalInput)
	}

	return TGDecimal{
		value: dValue,
		exp:   int32(exp),
	}, nil
}

// NewTGDecimalFromFloat converts a float64 to TGDecimal.
// NOTE: this will panic on NaN, +/-inf
func NewTGDecimalFromFloat(value float64) TGDecimal {
	if value == 0 {
		return NewTGDecimal(0, 0)
	}
	return convertFromFloat(value, math.Float64bits(value), &float64info)
}

// NewTGDecimalFromFloat converts a float32 to TGDecimal.
// NOTE: this will panic on NaN, +/-inf
func NewTGDecimalFromFloat32(value float32) TGDecimal {
	if value == 0 {
		return NewTGDecimal(0, 0)
	}
	// XOR is workaround for https://github.com/golang/go/issues/26285
	a := math.Float32bits(value) ^ 0x80808080
	return convertFromFloat(float64(value), uint64(a)^0x80808080, &float32info)
}

func convertFromFloat(val float64, bits uint64, flt *ieee754) TGDecimal {
	if math.IsNaN(val) || math.IsInf(val, 0) {
		panic(fmt.Sprintf("Cannot create a TGDecimal from %v", val))
	}
	exp := int(bits>>flt.mantissa) & (1<<flt.exponent - 1)
	mant := bits & (uint64(1)<<flt.mantissa - 1)

	switch exp {
	case 0:
		exp++
	default:
		// add implicit top bit
		mant |= uint64(1) << flt.mantissa
	}
	exp += flt.expBias

	var d internalDecimal
	d.Assign(mant)
	d.Shift(exp - int(flt.mantissa))
	d.negFlag = bits>>(flt.exponent+flt.mantissa) != 0

	roundShortest(&d, mant, exp, flt)
	// If less than 19 digits, we can do calculation in an int64.
	if d.usedDigits < 19 {
		tmp := int64(0)
		m := int64(1)
		for i := d.usedDigits - 1; i >= 0; i-- {
			tmp += m * int64(d.digits[i]-'0')
			m *= 10
		}
		if d.negFlag {
			tmp *= -1
		}
		return TGDecimal{value: big.NewInt(tmp), exp: int32(d.decPoint) - int32(d.usedDigits)}
	}
	dValue := new(big.Int)
	dValue, ok := dValue.SetString(string(d.digits[:d.usedDigits]), 10)
	if ok {
		return TGDecimal{value: dValue, exp: int32(d.decPoint) - int32(d.usedDigits)}
	}

	return NewTGDecimalFromFloatWithExponent(val, int32(d.decPoint)-int32(d.usedDigits))
}

// NewTGDecimalFromFloatWithExponent converts a float64 to TGDecimal, with an arbitrary number of fractional digits.
func NewTGDecimalFromFloatWithExponent(value float64, exp int32) TGDecimal {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		panic(fmt.Sprintf("Cannot create a TGDecimal from %v", value))
	}

	bits := math.Float64bits(value)
	mant := bits & (1<<52 - 1)
	exp2 := int32((bits >> 52) & (1<<11 - 1))
	sign := bits >> 63

	if exp2 == 0 {
		if mant == 0 {
			return TGDecimal{}
		} else {
			exp2++
		}
	} else {
		mant |= 1 << 52
	}

	exp2 -= 1023 + 52

	// normalizing base-2 values
	for mant&1 == 0 {
		mant = mant >> 1
		exp2++
	}

	// maximum number of fractional base-10 digits to represent 2^N exactly cannot be more than -N if N<0
	if exp < 0 && exp < exp2 {
		if exp2 < 0 {
			exp = exp2
		} else {
			exp = 0
		}
	}

	// TODO: Revisit later - for more testing and optimization
	// Split 10^M * 2^N as 5^M * 2^(M+N)
	exp2 -= exp

	temp := big.NewInt(1)
	dMant := big.NewInt(int64(mant))

	// Perform 5^M
	if exp > 0 {
		temp = temp.SetInt64(int64(exp))
		temp = temp.Exp(fiveInt, temp, nil)
	} else if exp < 0 {
		temp = temp.SetInt64(-int64(exp))
		temp = temp.Exp(fiveInt, temp, nil)
		dMant = dMant.Mul(dMant, temp)
		temp = temp.SetUint64(1)
	}

	// Perform 2^(M+N)
	if exp2 > 0 {
		dMant = dMant.Lsh(dMant, uint(exp2))
	} else if exp2 < 0 {
		temp = temp.Lsh(temp, uint(-exp2))
	}

	// rounding and downscaling
	if exp > 0 || exp2 < 0 {
		halfDown := new(big.Int).Rsh(temp, 1)
		dMant = dMant.Add(dMant, halfDown)
		dMant = dMant.Quo(dMant, temp)
	}

	if sign == 1 {
		dMant = dMant.Neg(dMant)
	}

	return TGDecimal{
		value: dMant,
		exp:   exp,
	}
}

// TODO: Revisit later - for more testing
// rescale returns a rescaled version of the internalDecimal.
// Returned internalDecimal may be less precise if the exponent is bigger than the initial exponent of the TGDecimal.
// NOTE: this will truncate, NOT round
func (d TGDecimal) rescale(exp int32) TGDecimal {
	d.ensureInitialized()
	diff := math.Abs(float64(exp) - float64(d.exp))
	value := new(big.Int).Set(d.value)

	expScale := new(big.Int).Exp(tenInt, big.NewInt(int64(diff)), nil)
	if exp > d.exp {
		value = value.Quo(value, expScale)
	} else if exp < d.exp {
		value = value.Mul(value, expScale)
	}

	return TGDecimal{
		value: value,
		exp:   exp,
	}
}

// Abs returns the absolute value of the internalDecimal.
func (d TGDecimal) Abs() TGDecimal {
	d.ensureInitialized()
	d2Value := new(big.Int).Abs(d.value)
	return TGDecimal{
		value: d2Value,
		exp:   d.exp,
	}
}

// Add returns digits + d2.
func (d TGDecimal) Add(d2 TGDecimal) TGDecimal {
	baseScale := min(d.exp, d2.exp)
	rd := d.rescale(baseScale)
	rd2 := d2.rescale(baseScale)

	d3Value := new(big.Int).Add(rd.value, rd2.value)
	return TGDecimal{
		value: d3Value,
		exp:   baseScale,
	}
}

// Subtract returns digits - d2.
func (d TGDecimal) Subtract(d2 TGDecimal) TGDecimal {
	baseScale := min(d.exp, d2.exp)
	rd := d.rescale(baseScale)
	rd2 := d2.rescale(baseScale)

	d3Value := new(big.Int).Sub(rd.value, rd2.value)
	return TGDecimal{
		value: d3Value,
		exp:   baseScale,
	}
}

// Negate returns -digits.
func (d TGDecimal) Negate() TGDecimal {
	d.ensureInitialized()
	val := new(big.Int).Neg(d.value)
	return TGDecimal{
		value: val,
		exp:   d.exp,
	}
}

// Multiply returns digits * d2.
func (d TGDecimal) Multiply(d2 TGDecimal) TGDecimal {
	d.ensureInitialized()
	d2.ensureInitialized()

	expInt64 := int64(d.exp) + int64(d2.exp)
	if expInt64 > math.MaxInt32 || expInt64 < math.MinInt32 {
		// NOTE(vadim): better to panic than give incorrect results, as
		// Decimals are usually used for money
		panic(fmt.Sprintf("exponent %v overflows an int32!", expInt64))
	}

	d3Value := new(big.Int).Mul(d.value, d2.value)
	return TGDecimal{
		value: d3Value,
		exp:   int32(expInt64),
	}
}

// Shift shifts the internalDecimal in base 10.
// It shifts left when shift is positive and right if shift is negative.
// In other words, the given value for shift is added to the exponent of the internalDecimal.
func (d TGDecimal) Shift(shift int32) TGDecimal {
	d.ensureInitialized()
	return TGDecimal{
		value: new(big.Int).Set(d.value),
		exp:   d.exp + shift,
	}
}

// Divide returns digits / d2. If it doesn't divide exactly, the result will have
// DivisionPrecision digits after the internalDecimal point.
func (d TGDecimal) Divide(d2 TGDecimal) TGDecimal {
	return d.DivideWithRounding(d2, int32(DivisionPrecision))
}

// QuotientRemainder returns quotient q and remainder r such that
//   digits = d2 * q + r, q an integer multiple of 10^(-precision)
//   0 <= r < abs(d2) * 10 ^(-precision) if digits>=0
//   0 >= r > -abs(d2) * 10 ^(-precision) if digits<0
func (d TGDecimal) QuotientRemainder(d2 TGDecimal, precision int32) (TGDecimal, TGDecimal) {
	d.ensureInitialized()
	d2.ensureInitialized()
	if d2.value.Sign() == 0 {
		panic("internalDecimal division by 0")
	}
	scale := -precision
	expScale := int64(d.exp - d2.exp - scale)
	if expScale > math.MaxInt32 || expScale < math.MinInt32 {
		panic("overflow in internalDecimal QuotientRemainder")
	}
	var bi1, bi2, expo big.Int
	var scaleExp int32
	if expScale < 0 {
		bi1 = *d.value
		expo.SetInt64(-expScale)
		bi2.Exp(tenInt, &expo, nil)
		bi2.Mul(d2.value, &bi2)
		scaleExp = d.exp
	} else {
		expo.SetInt64(expScale)
		bi1.Exp(tenInt, &expo, nil)
		bi1.Mul(d.value, &bi1)
		bi2 = *d2.value
		scaleExp = scale + d2.exp
	}
	var q, r big.Int
	q.QuoRem(&bi1, &bi2, &r)
	dQuotient := TGDecimal{value: &q, exp: scale}
	dRemainder := TGDecimal{value: &r, exp: scaleExp}
	return dQuotient, dRemainder
}

// TODO: Revisit later - for more testing
// DivideWithRounding divides and rounds to a given precision
// i.e. to an integer multiple of 10^(-precision)
//   for a positive quotient digit 5 is rounded up, away from 0
//   if the quotient is negative then digit 5 is rounded down, away from 0
func (d TGDecimal) DivideWithRounding(d2 TGDecimal, precision int32) TGDecimal {
	// QuotientRemainder already checks initialization
	quo, rem := d.QuotientRemainder(d2, precision)
	// the decision is based on comparing r*10^precision and d2/2
	var tempValue big.Int
	tempValue.Abs(rem.value)
	tempValue.Lsh(&tempValue, 1)
	// At this point, tempValue = abs(r.value) * 2
	r2 := TGDecimal{value: &tempValue, exp: rem.exp + precision}
	// r2 is now 2 * r * 10 ^ precision
	var c = r2.Compare(d2.Abs())

	if c < 0 {
		return quo
	}

	if d.value.Sign()*d2.value.Sign() < 0 {
		return quo.Subtract(NewTGDecimal(1, -precision))
	}

	return quo.Add(NewTGDecimal(1, -precision))
}

// Modulus returns digits % d2.
func (d TGDecimal) Modulus(d2 TGDecimal) TGDecimal {
	quo := d.Divide(d2).Truncate(0)
	return d.Subtract(d2.Multiply(quo))
}

// Power returns digits to the power d2
func (d TGDecimal) Power(d2 TGDecimal) TGDecimal {
	var temp TGDecimal
	if d2.IntegerPart() == 0 {
		return NewTGDecimalFromFloat(1)
	}
	temp = d.Power(d2.Divide(NewTGDecimalFromFloat(2)))
	if d2.IntegerPart()%2 == 0 {
		return temp.Multiply(temp)
	}
	if d2.IntegerPart() > 0 {
		return temp.Multiply(temp).Multiply(d)
	}
	return temp.Multiply(temp).Divide(d)
}

// Compare compares the numbers represented by digits and d2 and returns:
//     -1 when (digits < d2), 0 when (digits == d2), +1 when (digits > d2)
func (d TGDecimal) Compare(d2 TGDecimal) int {
	d.ensureInitialized()
	d2.ensureInitialized()

	if d.exp == d2.exp {
		return d.value.Cmp(d2.value)
	}

	baseExp := min(d.exp, d2.exp)
	rd := d.rescale(baseExp)
	rd2 := d2.rescale(baseExp)

	return rd.value.Cmp(rd2.value)
}

// Sign returns -1 when (digits <  0), 0 when (digits == 0), +1 when (digits >  0)
func (d TGDecimal) Sign() int {
	if d.value == nil {
		return 0
	}
	return d.value.Sign()
}

// IsPositive returns true when (digits > 0), false when (digits == 0), false when (digits < 0)
func (d TGDecimal) IsPositive() bool {
	return d.Sign() == 1
}

// IsNegative returns true when (digits < 0), false when (digits == 0), false when (digits > 0)
func (d TGDecimal) IsNegative() bool {
	return d.Sign() == -1
}

// IsZero returns true when (digits == 0), false when (digits > 0), false when (digits < 0)
func (d TGDecimal) IsZero() bool {
	return d.Sign() == 0
}

// Exponent returns the exponent, or scale component of the internalDecimal.
func (d TGDecimal) Exponent() int32 {
	return d.exp
}

// Coefficient returns the coefficient of the internalDecimal.  It is scaled by 10^Exponent()
func (d TGDecimal) Coefficient() *big.Int {
	return big.NewInt(0).Set(d.value)
}

// IntegerPart returns the integer component of the internalDecimal.
func (d TGDecimal) IntegerPart() int64 {
	scaledD := d.rescale(0)
	return scaledD.value.Int64()
}

// RationalNumber returns a rational number representation of the internalDecimal.
func (d TGDecimal) RationalNumber() *big.Rat {
	d.ensureInitialized()
	if d.exp <= 0 {
		denom := new(big.Int).Exp(tenInt, big.NewInt(-int64(d.exp)), nil)
		return new(big.Rat).SetFrac(d.value, denom)
	}

	mul := new(big.Int).Exp(tenInt, big.NewInt(int64(d.exp)), nil)
	num := new(big.Int).Mul(d.value, mul)
	return new(big.Rat).SetFrac(num, oneInt)
}

// Float64 returns the nearest float64 value for digits and a bool indicating whether f represents digits exactly.
func (d TGDecimal) Float64() (f float64, exact bool) {
	return d.RationalNumber().Float64()
}

// String returns the string representation of the internalDecimal with the fixed point.
func (d TGDecimal) String() string {
	return d.string(true)
}

// StringFixed returns a rounded fixed-point string with places digits after the internalDecimal point.
func (d TGDecimal) StringFixed(places int32) string {
	rounded := d.Round(places)
	return rounded.string(false)
}

// Round rounds the internalDecimal to noOfPlaces internalDecimal places.
// If noOfPlaces < 0, it will round the integer part to the nearest 10^(-noOfPlaces).
func (d TGDecimal) Round(noOfPlaces int32) TGDecimal {
	// truncate to places + 1
	ret := d.rescale(-noOfPlaces - 1)

	// add sign(digits) * 0.5
	if ret.value.Sign() < 0 {
		ret.value.Sub(ret.value, fiveInt)
	} else {
		ret.value.Add(ret.value, fiveInt)
	}

	// floor for positive numbers, ceil for negative numbers
	_, m := ret.value.DivMod(ret.value, tenInt, new(big.Int))
	ret.exp++
	if ret.value.Sign() < 0 && m.Cmp(zeroInt) != 0 {
		ret.value.Add(ret.value, oneInt)
	}

	return ret
}

// Floor returns the nearest integer value less than or equal to digits.
func (d TGDecimal) Floor() TGDecimal {
	d.ensureInitialized()

	if d.exp >= 0 {
		return d
	}

	exp := big.NewInt(10)

	// NOTE(vadim): must negate after casting to prevent int32 overflow
	exp.Exp(exp, big.NewInt(-int64(d.exp)), nil)

	z := new(big.Int).Div(d.value, exp)
	return TGDecimal{value: z, exp: 0}
}

// Ceil returns the nearest integer value greater than or equal to digits.
func (d TGDecimal) Ceil() TGDecimal {
	d.ensureInitialized()

	if d.exp >= 0 {
		return d
	}

	exp := big.NewInt(10)

	// NOTE(vadim): must negate after casting to prevent int32 overflow
	exp.Exp(exp, big.NewInt(-int64(d.exp)), nil)

	z, m := new(big.Int).DivMod(d.value, exp, new(big.Int))
	if m.Cmp(zeroInt) != 0 {
		z.Add(z, oneInt)
	}
	return TGDecimal{value: z, exp: 0}
}

// Truncate truncates off digits from the number, without rounding.
func (d TGDecimal) Truncate(precision int32) TGDecimal {
	d.ensureInitialized()
	if precision >= 0 && -precision > d.exp {
		return d.rescale(-precision)
	}
	return d
}

// UnmarshalJSON implements the json.Unmarshaler interface.
func (d *TGDecimal) UnmarshalJSON(decimalBytes []byte) error {
	if string(decimalBytes) == "null" {
		return nil
	}

	str, err := unquoteIfQuoted(decimalBytes)
	if err != nil {
		return fmt.Errorf("error decoding string '%s': %s", decimalBytes, err)
	}

	decimal, err := NewTGDecimalFromString(str)
	*d = decimal
	if err != nil {
		return fmt.Errorf("error decoding string '%s': %s", str, err)
	}
	return nil
}

// MarshalJSON implements the json.Marshaler interface.
func (d TGDecimal) MarshalJSON() ([]byte, error) {
	var str string
	if MarshalJSONWithoutQuotes {
		str = d.String()
	} else {
		str = "\"" + d.String() + "\""
	}
	return []byte(str), nil
}

// UnmarshalBinary implements the encoding.BinaryUnmarshaler interface. As a string representation
// is already used when encoding to text, this method stores that string as []byte
func (d *TGDecimal) UnmarshalBinary(data []byte) error {
	// Extract the exponent
	d.exp = int32(binary.BigEndian.Uint32(data[:4]))

	// Extract the value
	d.value = new(big.Int)
	return d.value.GobDecode(data[4:])
}

// MarshalBinary implements the encoding.BinaryMarshaler interface.
func (d TGDecimal) MarshalBinary() (data []byte, err error) {
	// Write the exponent first since it's a fixed size
	v1 := make([]byte, 4)
	binary.BigEndian.PutUint32(v1, uint32(d.exp))

	// Add the value
	var v2 []byte
	if v2, err = d.value.GobEncode(); err != nil {
		return
	}

	// Return the byte array
	data = append(v1, v2...)
	return
}

// UnmarshalText implements the encoding.TextUnmarshaler interface for XML deserialization.
func (d *TGDecimal) UnmarshalText(text []byte) error {
	str := string(text)

	dec, err := NewTGDecimalFromString(str)
	*d = dec
	if err != nil {
		return fmt.Errorf("error decoding string '%s': %s", str, err)
	}

	return nil
}

// MarshalText implements the encoding.TextMarshaler interface for XML serialization.
func (d TGDecimal) MarshalText() (text []byte, err error) {
	return []byte(d.String()), nil
}

// GobEncode implements the gob.GobEncoder interface for gob serialization.
func (d TGDecimal) GobEncode() ([]byte, error) {
	return d.MarshalBinary()
}

// GobDecode implements the gob.GobDecoder interface for gob serialization.
func (d *TGDecimal) GobDecode(data []byte) error {
	return d.UnmarshalBinary(data)
}

// StringScaled first scales the internalDecimal then calls .String() on it.
// NOTE: buggy, unintuitive, and DEPRECATED! Use StringFixed instead.
func (d TGDecimal) StringScaled(exp int32) string {
	return d.rescale(exp).String()
}

func (d TGDecimal) string(trimTrailingZeros bool) string {
	if d.exp >= 0 {
		return d.rescale(0).value.String()
	}

	abs := new(big.Int).Abs(d.value)
	str := abs.String()

	var intPart, fractionalPart string

	// NOTE(vadim): this cast to int will cause bugs if digits.exp == INT_MIN
	// and you are on a 32-bit machine. Won't fix this super-edge case.
	dExpInt := int(d.exp)
	if len(str) > -dExpInt {
		intPart = str[:len(str)+dExpInt]
		fractionalPart = str[len(str)+dExpInt:]
	} else {
		intPart = "0"

		num0s := -dExpInt - len(str)
		fractionalPart = strings.Repeat("0", num0s) + str
	}

	if trimTrailingZeros {
		i := len(fractionalPart) - 1
		for ; i >= 0; i-- {
			if fractionalPart[i] != '0' {
				break
			}
		}
		fractionalPart = fractionalPart[:i+1]
	}

	number := intPart
	if len(fractionalPart) > 0 {
		number += "." + fractionalPart
	}

	if d.value.Sign() < 0 {
		return "-" + number
	}

	return number
}

func (d *TGDecimal) ensureInitialized() {
	if d.value == nil {
		d.value = new(big.Int)
	}
}

func min(x, y int32) int32 {
	if x >= y {
		return y
	}
	return x
}

func unquoteIfQuoted(value interface{}) (string, error) {
	var bytes []byte

	switch v := value.(type) {
	case string:
		bytes = []byte(v)
	case []byte:
		bytes = v
	default:
		return "", fmt.Errorf("Could not convert value '%+v' to byte array of type '%T'",
			value, value)
	}

	// If the amount is quoted, strip the quotes
	if len(bytes) > 2 && bytes[0] == '"' && bytes[len(bytes)-1] == '"' {
		bytes = bytes[1 : len(bytes)-1]
	}
	return string(bytes), nil
}
