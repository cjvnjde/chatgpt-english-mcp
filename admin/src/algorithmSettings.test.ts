import assert from "node:assert/strict";
import test from "node:test";
import { settingFields, settingsDraft, validateSettings, type SettingsValues } from "./algorithmSettings.ts";

function exampleValues(): SettingsValues {
  const values: SettingsValues = Object.fromEntries(settingFields.map(field => [field.key,
    field.options ? field.options[0].value : field.list ? [] : field.percent ? field.min! / 100 : field.min!]));
  values.focusBatchSize = 10;
  return values;
}

test("focus mode and batch size survive the settings form round trip", () => {
  const values = exampleValues();
  values.learningMode = "focused";
  values.focusBatchSize = 17;
  const result = validateSettings(settingsDraft(values));
  assert.deepEqual(result.errors, {});
  assert.deepEqual(result.values, values);
});

test("focus controls reject unknown modes and invalid batch sizes", () => {
  for (const mode of ["", "random", "FOCUSED"]) {
    const draft = settingsDraft(exampleValues());
    draft.learningMode = mode;
    assert.ok(validateSettings(draft).errors.learningMode);
  }
  for (const size of ["", "0", "101", "2.5", "NaN"]) {
    const draft = settingsDraft(exampleValues());
    draft.focusBatchSize = size;
    assert.ok(validateSettings(draft).errors.focusBatchSize);
  }
  for (const size of ["1", "100"]) {
    const draft = settingsDraft(exampleValues());
    draft.focusBatchSize = size;
    assert.equal(validateSettings(draft).errors.focusBatchSize, undefined);
  }
});
