import { onCleanup, onMount, type JSX } from "solid-js";

export default function Modal(props: {
  title: string;
  close: () => void;
  children: JSX.Element;
  wide?: boolean;
}) {
  let dialog!: HTMLDialogElement;
  const previous = document.activeElement as HTMLElement | null;
  onMount(() => dialog.showModal());
  onCleanup(() => {
    dialog.close();
    previous?.focus();
  });
  return (
    <dialog
      ref={dialog}
      classList={{ wide: props.wide }}
      aria-label={props.title}
      onCancel={(e) => {
        e.preventDefault();
        props.close();
      }}
    >
      <div class="dialog-heading">
        <h2>{props.title}</h2>
        <button
          class="icon-button"
          aria-label="Close dialog"
          onClick={props.close}
        >
          ×
        </button>
      </div>
      {props.children}
    </dialog>
  );
}
