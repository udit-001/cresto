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

const ytdSummaryFixture = `{
  "data": [
    {"name": "BASIC", "total": 337500, "m4": 112500, "m5": 112500, "m6": 112500},
    {"name": "PF", "total": -40500, "m4": -13500, "m5": -13500, "m6": -13500}
  ],
  "payrolls": [{"code": "m4", "description": "Apr 2026"}, {"code": "m5", "description": "May 2026"}, {"code": "m6", "description": "Jun 2026"}]
}`

func TestParseYTDSummary(t *testing.T) {
	items, err := parseYTDSummary([]byte(ytdSummaryFixture))
	if err != nil {
		t.Fatalf("parseYTDSummary: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0]["name"] != "BASIC" {
		t.Errorf("first item name = %v, want BASIC", items[0]["name"])
	}
}

func TestYTDSummary_YTDForMonth(t *testing.T) {
	items, _ := parseYTDSummary([]byte(ytdSummaryFixture))
	summary := &YTDSummary{items: items}

	// April (first month of FY): YTD = just April
	ytdApril := summary.YTDForMonth(4)
	if ytdApril["BASIC"] != 112500 {
		t.Errorf("April BASIC YTD = %v, want 112500", ytdApril["BASIC"])
	}

	// June (m6): YTD = Apr + May + Jun
	ytdJune := summary.YTDForMonth(6)
	if ytdJune["BASIC"] != 337500 {
		t.Errorf("June BASIC YTD = %v, want 337500", ytdJune["BASIC"])
	}
	if ytdJune["PF"] != -40500 {
		t.Errorf("June PF YTD = %v, want -40500", ytdJune["PF"])
	}

	// May (m5): YTD = Apr + May
	ytdMay := summary.YTDForMonth(5)
	if ytdMay["BASIC"] != 225000 {
		t.Errorf("May BASIC YTD = %v, want 225000", ytdMay["BASIC"])
	}
}

func TestForm16Part(t *testing.T) {
	cases := []struct{ title, want string }{
		{"Form16-2025-2026 Part A", "A"},
		{"Form16-2025-2026 Part B", "B"},
		{"Form16-2024-2025 PART B", "B"},
		{"Form16-2025-2026 part a", "A"},
		{"Some random document", "A"},
	}
	for _, c := range cases {
		if got := form16Part(c.title); got != c.want {
			t.Errorf("form16Part(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}
