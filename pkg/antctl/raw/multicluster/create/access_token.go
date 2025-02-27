// Copyright 2022 Antrea Authors
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

package create

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"antrea.io/antrea/pkg/antctl/raw/multicluster/common"
)

type memberTokenOptions struct {
	namespace string
	output    string
}

var memberTokenOpts *memberTokenOptions

var memberTokenExamples = strings.Trim(`
# Create a member token.
  $ antctl mc create membertoken cluster-east-token -n antrea-multicluster
# Create a member token and save the Secret manifest to a file.
  $ antctl mc create membertoken cluster-east-token -n antrea-multicluster -o token-secret.yml
`, "\n")

func (o *memberTokenOptions) validateAndComplete() error {
	if o.namespace == "" {
		return fmt.Errorf("the Namespace is required")
	}
	return nil
}

func NewAccessTokenCmd() *cobra.Command {
	command := &cobra.Command{
		Use:     "membertoken",
		Args:    cobra.MaximumNArgs(1),
		Short:   "Create a member token in a leader cluster",
		Long:    "Create a member token in a leader cluster, which will be saved in a Secret. A ServiceAccount and a RoleBinding will be created too if they do not exist.",
		Example: memberTokenExamples,
		RunE:    memberTokenRunE,
	}

	o := &memberTokenOptions{}
	memberTokenOpts = o
	command.Flags().StringVarP(&o.namespace, "namespace", "n", "", "Namespace of the ClusterSet")
	command.Flags().StringVarP(&o.output, "output-file", "o", "", "Output file to save the token Secret manifest")

	return command
}

func memberTokenRunE(cmd *cobra.Command, args []string) error {
	if err := memberTokenOpts.validateAndComplete(); err != nil {
		return err
	}
	if len(args) != 1 {
		return fmt.Errorf("exactly one NAME is required, got %d", len(args))
	}
	k8sClient, err := common.NewClient(cmd)
	if err != nil {
		return err
	}

	var createErr error
	createdRes := []map[string]interface{}{}
	defer func() {
		if createErr != nil {
			if err := common.Rollback(cmd, k8sClient, createdRes); err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Failed to rollback: %v\n", err)
			}
		}
	}()

	var file *os.File
	if memberTokenOpts.output != "" {
		if file, err = os.OpenFile(memberTokenOpts.output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600); err != nil {
			return err
		}
	}
	defer file.Close()

	if createErr = common.CreateMemberToken(cmd, k8sClient, args[0], memberTokenOpts.namespace, file, &createdRes); createErr != nil {
		return createErr
	}

	fmt.Fprintf(cmd.OutOrStdout(), "You can now run the \"antctl mc join\" command with the token to have the cluster join the ClusterSet\n")

	return nil
}
