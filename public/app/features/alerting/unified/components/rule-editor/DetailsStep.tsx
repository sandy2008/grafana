import { css } from '@emotion/css';
import React from 'react';
import { useFormContext } from 'react-hook-form';

import { GrafanaTheme2 } from '@grafana/data';
import { useStyles2, Field, Input } from '@grafana/ui';

import { RuleFormType, RuleFormValues } from '../../types/rule-form';

import AnnotationsField from './AnnotationsField';
import { GroupAndNamespaceFields } from './GroupAndNamespaceFields';
import { RuleEditorSection } from './RuleEditorSection';
import { checkForPathSeparator } from './util';

const recordingRuleNameValidationPattern = {
  message:
    'Recording rule name must be valid metric name. It may only contain letters, numbers, and colons. It may not contain whitespace.',
  value: /^[a-zA-Z_:][a-zA-Z0-9_:]*$/,
};

export function DetailsStep() {
  const {
    register,
    watch,
    formState: { errors },
  } = useFormContext<RuleFormValues & { location?: string }>();

  const styles = useStyles2(getStyles);

  const ruleFormType = watch('type');
  const dataSourceName = watch('dataSourceName');
  const type = watch('type');

  return (
    <RuleEditorSection
      stepNo={type === RuleFormType.cloudRecording ? 2 : 3}
      title={
        type === RuleFormType.cloudRecording ? 'Add details for your recording rule' : 'Add details for your alert'
      }
      description={
        type === RuleFormType.cloudRecording
          ? 'Add labels to help you better manage your rules'
          : 'Write a summary and add labels to help you better manage your alerts'
      }
    >
      <Field
        className={styles.formInput}
        label="Rule name"
        error={errors?.name?.message}
        invalid={!!errors.name?.message}
      >
        <Input
          id="name"
          {...register('name', {
            required: { value: true, message: 'Must enter an alert name' },
            pattern: ruleFormType === RuleFormType.cloudRecording ? recordingRuleNameValidationPattern : undefined,
            validate: {
              pathSeparator: (value: string) => {
                // we use the alert rule name as the "groupname" for Grafana managed alerts, so we can't allow path separators
                if (ruleFormType === RuleFormType.grafana) {
                  return checkForPathSeparator(value);
                }

                return true;
              },
            },
          })}
        />
      </Field>

      {(ruleFormType === RuleFormType.cloudRecording || ruleFormType === RuleFormType.cloudAlerting) &&
        dataSourceName && <GroupAndNamespaceFields rulesSourceName={dataSourceName} />}

      {type !== RuleFormType.cloudRecording && <AnnotationsField />}
    </RuleEditorSection>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  formInput: css`
    width: 275px;

    & + & {
      margin-left: ${theme.spacing(3)};
    }
  `,
});
