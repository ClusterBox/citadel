package initcmd

import "github.com/charmbracelet/huh"

type huhPrompter struct{}

// NewHuhPrompter returns the interactive terminal Prompter.
func NewHuhPrompter() Prompter { return huhPrompter{} }

func (huhPrompter) Select(title string, options []string, def string) (string, error) {
	v := def
	err := huh.NewSelect[string]().Title(title).Options(huh.NewOptions(options...)...).Value(&v).Run()
	return v, err
}

func (huhPrompter) Input(title, def string, validate func(string) error) (string, error) {
	v := def
	in := huh.NewInput().Title(title).Value(&v)
	if validate != nil {
		in = in.Validate(validate)
	}
	err := in.Run()
	return v, err
}
