import { Index } from "solid-js";

export default function TextList(props: {
  label: string;
  values: string[];
  change: (values: string[]) => void;
}) {
  return (
    <div class="text-list">
      <div class="toolbar">
        <strong>{props.label}</strong>
        <button
          type="button"
          onClick={() => props.change([...props.values, ""])}
        >
          + Add
        </button>
      </div>
      <Index each={props.values}>
        {(value, index) => (
          <div class="text-list-row">
            <textarea
              aria-label={`${props.label} ${index + 1}`}
              rows="2"
              value={value()}
              onInput={(e) =>
                props.change(
                  props.values.map((v, i) =>
                    i === index ? e.currentTarget.value : v,
                  ),
                )
              }
            />
            <button
              type="button"
              aria-label={`Remove ${props.label.toLowerCase()} ${index + 1}`}
              onClick={() =>
                props.change(props.values.filter((_, i) => i !== index))
              }
            >
              Remove
            </button>
          </div>
        )}
      </Index>
    </div>
  );
}
