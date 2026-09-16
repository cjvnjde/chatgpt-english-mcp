import { onCleanup, onMount, type JSX } from "solid-js";

export default function Modal(props: {
  title: string;
  close: () => void;
  children: JSX.Element;
  wide?: boolean;
  busy?: boolean;
}) {
  let dialog!: HTMLDialogElement;
  let backdropPointer = false;
  const previous = document.activeElement as HTMLElement | null;
  onMount(() => dialog.showModal());
  onCleanup(() => {
    dialog.close();
    if (previous?.isConnected) previous.focus();
  });
  return (
    <dialog
      ref={dialog}
      classList={{ wide: props.wide }}
      aria-label={props.title}
      onCancel={(e) => {
        e.preventDefault();
        if (!props.busy) props.close();
      }}
      onPointerDown={(event) => {
        const bounds = event.currentTarget.getBoundingClientRect();
        backdropPointer =
          event.target === event.currentTarget &&
          (event.clientX < bounds.left ||
            event.clientX > bounds.right ||
            event.clientY < bounds.top ||
            event.clientY > bounds.bottom);
      }}
      onClick={(event) => {
        if (
          backdropPointer &&
          event.target === event.currentTarget &&
          !props.busy
        )
          props.close();
        backdropPointer = false;
      }}
    >
      <div class="dialog-heading">
        <h2>{props.title}</h2>
        <button
          class="icon-button"
          aria-label="Close dialog"
          disabled={props.busy}
          onClick={props.close}
        >
          ×
        </button>
      </div>
      {props.children}
    </dialog>
  );
}
