// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package crap

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

type jacocoCounter struct {
	Type    string `xml:"type,attr"`
	Missed  int    `xml:"missed,attr"`
	Covered int    `xml:"covered,attr"`
}

type jacocoMethod struct {
	Name     string          `xml:"name,attr"`
	Desc     string          `xml:"desc,attr"`
	Counters []jacocoCounter `xml:"counter"`
}

type jacocoClass struct {
	Name    string         `xml:"name,attr"`
	Methods []jacocoMethod `xml:"method"`
}

type jacocoPackage struct {
	Classes []jacocoClass `xml:"class"`
}

type jacocoReport struct {
	Packages []jacocoPackage `xml:"package"`
}

// ErrNoComplexity means the report carries no COMPLEXITY counters at all.
var ErrNoComplexity = errors.New("jacoco report has no COMPLEXITY counters")

// ParseJaCoCo reads a JaCoCo XML report. CC is the method's COMPLEXITY
// counter; coverage is its INSTRUCTION counter.
func ParseJaCoCo(r io.Reader) ([]Row, error) {
	var rep jacocoReport
	if err := xml.NewDecoder(r).Decode(&rep); err != nil {
		return nil, fmt.Errorf("parse jacoco xml: %w", err)
	}
	var rows []Row
	for _, p := range rep.Packages {
		for _, c := range p.Classes {
			rows = append(rows, classRows(c)...)
		}
	}
	if len(rows) == 0 {
		return nil, ErrNoComplexity
	}
	return rows, nil
}

func classRows(c jacocoClass) []Row {
	class := strings.ReplaceAll(c.Name, "/", ".")
	var rows []Row
	for _, m := range c.Methods {
		cc, ok := counter(m.Counters, "COMPLEXITY")
		if !ok {
			continue
		}
		ins, _ := counter(m.Counters, "INSTRUCTION")
		rows = append(rows, Row{
			Name:     class + "." + m.Name + m.Desc,
			CC:       cc.Missed + cc.Covered,
			Coverage: ratio(ins.Covered, ins.Missed+ins.Covered),
		})
	}
	return rows
}

func counter(cs []jacocoCounter, typ string) (jacocoCounter, bool) {
	for _, c := range cs {
		if c.Type == typ {
			return c, true
		}
	}
	return jacocoCounter{}, false
}

func ratio(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}
