// Copyright 2019 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package tpm2

func (t *tpmContext) SelfTest(fullTest bool) error {
	return t.RunCommand(CommandSelfTest, nil, Separator, fullTest)
}

func (t *tpmContext) IncrementalSelfTest(toTest AlgorithmList) (AlgorithmList, error) {
	var toDoList AlgorithmList
	if err := t.RunCommand(CommandIncrementalSelfTest, nil, Separator, toTest, Separator, Separator,
		&toDoList); err != nil {
		return nil, err
	}
	return toDoList, nil
}

func (t *tpmContext) GetTestResult() (MaxBuffer, ResponseCode, error) {
	var outData MaxBuffer
	var testResult ResponseCode
	if err := t.RunCommand(CommandGetTestResult, nil, Separator, Separator, Separator, &outData,
		&testResult); err != nil {
		return nil, 0, err
	}
	return outData, testResult, nil
}
