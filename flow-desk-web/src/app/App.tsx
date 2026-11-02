import {useEffect, useState} from 'react';
import {Alert, Button, Card, Form, Input, Space, Spin, Tag} from 'antd';
import {api, login, logout, refresh, type User} from '../shared/api/client';

export function App() {
 const [user,setUser]=useState<User|null>(null);
 const [loading,setLoading]=useState(true);
 const [error,setError]=useState('');
 const load=async()=>setUser(await api<User>('/auth/me'));
 useEffect(()=>{refresh().then(load).catch(()=>{}).finally(()=>setLoading(false));},[]);
 if(loading)return <div className="shell"><Spin aria-label="正在恢复会话"/></div>;
 if(!user)return <main className="shell login"><div className="brand">FlowDesk <span className="muted">智办台</span></div><p className="subtitle">让每一次服务都有依据、有进展、有回执。</p><Card><Form layout="vertical" onFinish={async(values)=>{setError('');setLoading(true);try{await login(values.account,values.password);await load();}catch(e){setError((e as Error).message);}finally{setLoading(false);}}}><Form.Item name="account" label="工作账号" rules={[{required:true}]}><Input autoComplete="username"/></Form.Item><Form.Item name="password" label="密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>{error&&<Alert title={error} type="error"/>}<Button type="primary" htmlType="submit" block>登录工作台</Button></Form></Card></main>;
 return <main className="shell"><header className="topbar"><div><div className="brand">FlowDesk <span className="muted">/ 工作台</span></div><p className="subtitle">从问题到解决，让流程清晰可见。</p></div><Space><Tag>{user.role}</Tag><span>{user.display_name}</span><Button onClick={async()=>{await logout();setUser(null);}}>退出</Button></Space></header><section className="cards"><Card title="智能办理"><p>用自然语言查询订单、检索政策并办理工单。</p><Tag>工程基础已就绪</Tag></Card><Card title="业务与权限"><p>身份、租户与业务操作由统一服务管理。</p><Tag color="green">会话已验证</Tag></Card><Card title="知识与依据"><p>每条结论都能追溯到有效的业务事实。</p><Tag>按阶段接入</Tag></Card></section></main>;
}
