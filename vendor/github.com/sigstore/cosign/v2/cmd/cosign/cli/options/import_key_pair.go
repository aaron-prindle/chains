//
// Copyright 2021 The Sigstore Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package options

import (
	"github.com/spf13/cobra"
)

// ImportKeyPairOptions is the top level wrapper for the import-key-pair command.
type ImportKeyPairOptions struct {
	// Local key file generated by external program such as OpenSSL
	Key string

	// Filename used for outputted keys
	OutputKeyPrefix string

	SkipConfirmation bool
}

var _ Interface = (*ImportKeyPairOptions)(nil)

// AddFlags implements Interface
func (o *ImportKeyPairOptions) AddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&o.Key, "key", "k", "",
		"import key pair to use for signing")
	_ = cmd.Flags().SetAnnotation("key", cobra.BashCompFilenameExt, []string{})

	cmd.Flags().StringVarP(&o.OutputKeyPrefix, "output-key-prefix", "o", "import-cosign",
		"name used for outputted key pairs")
	_ = cmd.Flags().SetAnnotation("output-key-prefix", cobra.BashCompFilenameExt, []string{})

	cmd.Flags().BoolVarP(&o.SkipConfirmation, "yes", "y", false,
		"skip confirmation prompts for overwriting existing key")
}
