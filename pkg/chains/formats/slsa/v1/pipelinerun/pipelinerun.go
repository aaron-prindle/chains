/*
Copyright 2022 The Tekton Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pipelinerun

import (
	"time"

	intoto "github.com/in-toto/in-toto-golang/in_toto"
	slsa "github.com/in-toto/in-toto-golang/in_toto/slsa_provenance/v0.2"
	"github.com/tektoncd/chains/pkg/artifacts"
	"github.com/tektoncd/chains/pkg/chains/formats/slsa/attest"
	"github.com/tektoncd/chains/pkg/chains/formats/slsa/extract"
	"github.com/tektoncd/chains/pkg/chains/formats/slsa/v1/internal/material"
	"github.com/tektoncd/chains/pkg/chains/formats/slsa/v1/taskrun"
	"github.com/tektoncd/chains/pkg/chains/objects"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
)

type BuildConfig struct {
	Tasks []TaskAttestation `json:"tasks"`
}

type TaskAttestation struct {
	Name       string                    `json:"name,omitempty"`
	After      []string                  `json:"after,omitempty"`
	Ref        v1beta1.TaskRef           `json:"ref,omitempty"`
	StartedOn  time.Time                 `json:"startedOn,omitempty"`
	FinishedOn time.Time                 `json:"finishedOn,omitempty"`
	Status     string                    `json:"status,omitempty"`
	Steps      []attest.StepAttestation  `json:"steps,omitempty"`
	Invocation slsa.ProvenanceInvocation `json:"invocation,omitempty"`
	Results    []v1beta1.TaskRunResult   `json:"results,omitempty"`
}

func GenerateAttestation(builderID string, pro *objects.PipelineRunObject, logger *zap.SugaredLogger) (interface{}, error) {
	subjects := extract.SubjectDigests(pro, logger)

	mat, err := materials(pro, logger)
	if err != nil {
		return nil, err
	}
	att := intoto.ProvenanceStatement{
		StatementHeader: intoto.StatementHeader{
			Type:          intoto.StatementInTotoV01,
			PredicateType: slsa.PredicateSLSAProvenance,
			Subject:       subjects,
		},
		Predicate: slsa.ProvenancePredicate{
			Builder: slsa.ProvenanceBuilder{
				ID: builderID,
			},
			BuildType:   pro.GetGVK(),
			Invocation:  invocation(pro),
			BuildConfig: buildConfig(pro, logger),
			Metadata:    metadata(pro),
			Materials:   mat,
		},
	}
	return att, nil
}

func invocation(pro *objects.PipelineRunObject) slsa.ProvenanceInvocation {
	var paramSpecs []v1beta1.ParamSpec
	if ps := pro.Status.PipelineSpec; ps != nil {
		paramSpecs = ps.Params
	}
	var source *v1beta1.ConfigSource
	if p := pro.Status.Provenance; p != nil {
		source = p.ConfigSource
	}
	return attest.Invocation(source, pro.Spec.Params, paramSpecs, pro.GetObjectMeta())
}

func buildConfig(pro *objects.PipelineRunObject, logger *zap.SugaredLogger) BuildConfig {
	tasks := []TaskAttestation{}

	pSpec := pro.Status.PipelineSpec
	if pSpec == nil {
		return BuildConfig{}
	}
	pipelineTasks := append(pSpec.Tasks, pSpec.Finally...)

	var last string
	for i, t := range pipelineTasks {
		tr := pro.GetTaskRunFromTask(t.Name)

		// Ignore Tasks that did not execute during the PipelineRun.
		if tr == nil || tr.Status.CompletionTime == nil {
			logger.Infof("taskrun status not found for task %s", t.Name)
			continue
		}
		steps := []attest.StepAttestation{}
		for i, stepState := range tr.Status.Steps {
			step := tr.Status.TaskSpec.Steps[i]
			steps = append(steps, attest.Step(&step, &stepState))
		}
		after := t.RunAfter

		// Establish task order by retrieving all task's referenced
		// in the "when" and "params" fields
		refs := v1beta1.PipelineTaskResultRefs(&t)
		for _, ref := range refs {

			// Ensure task doesn't already exist in after
			found := false
			for _, at := range after {
				if at == ref.PipelineTask {
					found = true
				}
			}
			if !found {
				after = append(after, ref.PipelineTask)
			}
		}

		// tr is a finally task without an explicit runAfter value. It must have executed
		// after the last non-finally task, if any non-finally tasks were executed.
		if len(after) == 0 && i >= len(pSpec.Tasks) && last != "" {
			after = append(after, last)
		}
		params := tr.Spec.Params
		var paramSpecs []v1beta1.ParamSpec
		if tr.Status.TaskSpec != nil {
			paramSpecs = tr.Status.TaskSpec.Params
		} else {
			paramSpecs = []v1beta1.ParamSpec{}
		}

		// source information in taskrun status
		var source *v1beta1.ConfigSource
		if p := tr.Status.Provenance; p != nil {
			source = p.ConfigSource
		}

		task := TaskAttestation{
			Name:       t.Name,
			After:      after,
			StartedOn:  tr.Status.StartTime.Time.UTC(),
			FinishedOn: tr.Status.CompletionTime.Time.UTC(),
			Status:     getStatus(tr.Status.Conditions),
			Steps:      steps,
			Invocation: attest.Invocation(source, params, paramSpecs, &tr.ObjectMeta),
			Results:    tr.Status.TaskRunResults,
		}

		if t.TaskRef != nil {
			task.Ref = *t.TaskRef
		}

		tasks = append(tasks, task)
		if i < len(pSpec.Tasks) {
			last = task.Name
		}
	}
	return BuildConfig{Tasks: tasks}
}

func metadata(pro *objects.PipelineRunObject) *slsa.ProvenanceMetadata {
	m := &slsa.ProvenanceMetadata{}
	if pro.Status.StartTime != nil {
		utc := pro.Status.StartTime.Time.UTC()
		m.BuildStartedOn = &utc
	}
	if pro.Status.CompletionTime != nil {
		utc := pro.Status.CompletionTime.Time.UTC()
		m.BuildFinishedOn = &utc
	}
	for label, value := range pro.Labels {
		if label == attest.ChainsReproducibleAnnotation && value == "true" {
			m.Reproducible = true
		}
	}
	return m
}

// add any Git specification to materials
func materials(pro *objects.PipelineRunObject, logger *zap.SugaredLogger) ([]slsa.ProvenanceMaterial, error) {
	var mats []slsa.ProvenanceMaterial
	if p := pro.Status.Provenance; p != nil {
		m := slsa.ProvenanceMaterial{
			URI:    p.ConfigSource.URI,
			Digest: p.ConfigSource.Digest,
		}
		mats = append(mats, m)
	}
	pSpec := pro.Status.PipelineSpec
	if pSpec != nil {
		pipelineTasks := append(pSpec.Tasks, pSpec.Finally...)
		for _, t := range pipelineTasks {
			tr := pro.GetTaskRunFromTask(t.Name)
			// Ignore Tasks that did not execute during the PipelineRun.
			if tr == nil || tr.Status.CompletionTime == nil {
				logger.Infof("taskrun status not found for task %s", t.Name)
				continue
			}

			// add step images
			if err := taskrun.AddStepImagesToMaterials(tr.Status.Steps, &mats); err != nil {
				return mats, nil
			}

			// add sidecar images
			if err := taskrun.AddSidecarImagesToMaterials(tr.Status.Sidecars, &mats); err != nil {
				return mats, nil
			}

			// add remote task configsource information in materials
			if tr.Status.Provenance != nil && tr.Status.Provenance.ConfigSource != nil {
				m := slsa.ProvenanceMaterial{
					URI:    tr.Status.Provenance.ConfigSource.URI,
					Digest: tr.Status.Provenance.ConfigSource.Digest,
				}
				mats = append(mats, m)
			}
		}
	}
	var commit, url string
	// search spec.params
	for _, p := range pro.Spec.Params {
		if p.Name == attest.CommitParam {
			commit = p.Value.StringVal
			continue
		}
		if p.Name == attest.URLParam {
			url = p.Value.StringVal
		}
	}

	sms := artifacts.RetrieveMaterialsFromStructuredResults(pro, artifacts.ArtifactsInputsResultName, logger)
	mats = append(mats, sms...)

	// search status.PipelineSpec.params
	if pro.Status.PipelineSpec != nil {
		for _, p := range pro.Status.PipelineSpec.Params {
			if p.Default == nil {
				continue
			}
			if p.Name == attest.CommitParam {
				commit = p.Default.StringVal
				continue
			}
			if p.Name == attest.URLParam {
				url = p.Default.StringVal
			}
		}
	}

	// search status.PipelineRunResults
	for _, r := range pro.Status.PipelineResults {
		if r.Name == attest.CommitParam {
			commit = r.Value.StringVal
		}
		if r.Name == attest.URLParam {
			url = r.Value.StringVal
		}
	}
	if len(commit) > 0 && len(url) > 0 {
		url = attest.SPDXGit(url, "")
		mats = append(mats, slsa.ProvenanceMaterial{
			URI:    url,
			Digest: map[string]string{"sha1": commit},
		})
	}

	// remove duplicate materials
	mats, err := material.RemoveDuplicateMaterials(mats)
	if err != nil {
		return mats, err
	}
	return mats, nil
}

// Following tkn cli's behavior
// https://github.com/tektoncd/cli/blob/6afbb0f0dbc7186898568f0d4a0436b8b2994d99/pkg/formatted/k8s.go#L55
func getStatus(conditions []apis.Condition) string {
	var status string
	if len(conditions) > 0 {
		switch conditions[0].Status {
		case corev1.ConditionFalse:
			status = "Failed"
		case corev1.ConditionTrue:
			status = "Succeeded"
		case corev1.ConditionUnknown:
			status = "Running" // Should never happen
		}
	}
	return status
}