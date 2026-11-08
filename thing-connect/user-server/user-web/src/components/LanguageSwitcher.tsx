import { Dropdown, Button } from 'antd';
import { useRef, useState } from 'react';
import { useLanguage } from '../lib/i18n';

export function LanguageSwitcher() {
  const { language, canSwitch } = useLanguage();
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  if (!canSwitch) return null;
  return <Dropdown destroyOnHidden trigger={['click']} open={open} onOpenChange={setOpen} menu={{selectable:true,selectedKeys:[language],items:[{key:'zh-CN',label:'简体中文'},{key:'en',label:'English'}],onClick:({key})=>{window.WebI18n?.setLanguage(key);setOpen(false);trigger.current?.focus();}}}>
    <Button ref={trigger} type="text" htmlType="button" className="language-trigger" aria-label="语言 / Language" aria-expanded={open} onKeyDown={event=>{if(event.key==='Escape')setOpen(false);}}>
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18M5 6.5h14M5 17.5h14"/></svg>
      <span className="language-label">{language === 'en' ? 'English' : '简体中文'}</span><span className="language-chevron" aria-hidden="true">⌄</span>
    </Button>
  </Dropdown>;
}
