package greythr

import "testing"

const designationFixture = `{"panelData":{"currentposition":[{"empty":false,"sections":{"current-profile":{"empty":false,"profile":null,"data":[{"currentTransitions$extEmpInfo$c_division":null,"currentTransitions$extEmpInfo$c_costcenter":null,"currentTransitions$extEmpInfo$c_department":null,"currentTransitions$extEmpInfo$c_grade":null,"currentTransitions$extEmpInfo$c_designation":"Senior Consultant","basicInformation$orgTree$managername":null,"currentTransitions$extEmpInfo$c_location":"Bangalore"}],"footer":null,"reviewInfo":null}},"header":{}}],"employment-timeline":[{"empty":false,"sections":{"PREVIOUS":{"empty":false,"profile":"previous_emp_timeline","data":[],"footer":{},"reviewInfo":null}},"header":{}}]}}`

// personalDataFixture mirrors the /v3/api/empinfo/personal/data/{id} shape:
// a top-level object keyed by panel name, each value an array of panel
// objects whose sections hold a data array of flat key→value rows.
const personalDataFixture = `{
  "address": [{"header":{},"empty":false,"sections":{"address":{"empty":false,"profile":null,"data":[{"allAddress$addr$name":"Udit Mittal","allAddress$addr$mobile":"8826422842"}],"footer":null,"reviewInfo":null}}}],
  "profile": [{"header":{},"empty":false,"sections":{"profile":{"empty":false,"profile":null,"data":[{"basicInformation$emp1$name":"Udit Mittal","basicInformation$emp1$employeeno":"2792","basicInformation$emp1$email":"udit@gyansys.com"}],"footer":null,"reviewInfo":null}}}]
}`

func TestParseDesignation(t *testing.T) {
	got, err := parseDesignation([]byte(designationFixture))
	if err != nil {
		t.Fatalf("parseDesignation: %v", err)
	}
	if got != "Senior Consultant" {
		t.Errorf("designation = %q, want %q", got, "Senior Consultant")
	}
}

func TestParseDesignation_Absent(t *testing.T) {
	// Designation field is null — should return "" with no error.
	body := []byte(`{"panelData":{"currentposition":[{"sections":{"current-profile":{"data":[{"currentTransitions$extEmpInfo$c_designation":null}]}}}]}}`)
	got, err := parseDesignation(body)
	if err != nil {
		t.Fatalf("parseDesignation: %v", err)
	}
	if got != "" {
		t.Errorf("designation = %q, want empty", got)
	}
}

func TestParseDesignation_Garbage(t *testing.T) {
	if _, err := parseDesignation([]byte("not json")); err == nil {
		t.Fatal("want error for invalid JSON, got nil")
	}
}

func TestParseEmployeeNo(t *testing.T) {
	got, err := parseEmployeeNo([]byte(personalDataFixture))
	if err != nil {
		t.Fatalf("parseEmployeeNo: %v", err)
	}
	if got != "2792" {
		t.Errorf("employeeNo = %q, want %q", got, "2792")
	}
}

func TestParseEmployeeNo_Absent(t *testing.T) {
	// No employeeno field in any panel — should return "" with no error.
	body := []byte(`{"address":[{"sections":{"address":{"data":[{"allAddress$addr$name":"Udit"}]}}}]}`)
	got, err := parseEmployeeNo(body)
	if err != nil {
		t.Fatalf("parseEmployeeNo: %v", err)
	}
	if got != "" {
		t.Errorf("employeeNo = %q, want empty", got)
	}
}

func TestParseEmployeeNo_Garbage(t *testing.T) {
	if _, err := parseEmployeeNo([]byte("not json")); err == nil {
		t.Fatal("want error for invalid JSON, got nil")
	}
}
