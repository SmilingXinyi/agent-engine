package protocol

import "fmt"

func (options Options) Validate() error {
	if options.Input == nil {
		return fmt.Errorf("CLI input is required")
	}
	if options.Output == nil {
		return fmt.Errorf("CLI output is required")
	}
	return nil
}
