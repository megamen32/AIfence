package answer

type Control struct {
	Product string  `json:"название"`
	Change  float64 `json:"величина_%"`
}

type Evidence struct {
	Source string `json:"источник"`
	Ref    string `json:"локатор"`
	Fact   string `json:"наблюдение"`
}

type Input struct {
	Decision string     `json:"решение"`
	Change   *float64   `json:"величина_%"`
	Control  *Control   `json:"контроль"`
	Evidence []Evidence `json:"доказательства"`
}
