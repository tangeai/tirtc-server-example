import { Component, createRef, type Ref } from 'react';
import { Input, type InputRef } from 'antd';
import type { PasswordProps } from 'antd/es/input/Password';

type Props = PasswordProps & { password?: boolean; nativeRef?: Ref<HTMLInputElement> };
// Password managers may change the DOM without an input event. Preserve that
// visible value across parent/status renders; authentication reads the form DOM.
// Ant Design still owns rendering, styling and ordinary input interaction.
export class CredentialInput extends Component<Props> {
  private input = createRef<InputRef>();
  state = { revision: 0 };
  private sync = () => this.setState(({ revision }: { revision: number }) => ({ revision: revision + 1 }));
  getSnapshotBeforeUpdate() { return this.input.current?.input?.value; }
  componentDidUpdate(_props: Props, _state: unknown, value?: string) {
    const input = this.input.current?.input;
    if (input && value !== undefined && this.props.value === undefined) input.value = value;
  }
  render() {
    const { password, nativeRef, ...props } = this.props;
    const Control = password ? Input.Password : Input;
    return <Control {...props} value={props.value ?? this.input.current?.input?.value ?? props.defaultValue ?? ''}
      onFocus={event=>{props.onFocus?.(event);this.sync();}}
      onBlur={event=>{props.onBlur?.(event);this.sync();}}
      onChange={event=>{props.onChange?.(event);this.sync();}} ref={ref => {
      this.input.current = ref;
      if (typeof nativeRef === 'function') nativeRef(ref?.input ?? null);
      else if (nativeRef) nativeRef.current = ref?.input ?? null;
    }}/>;
  }
}
