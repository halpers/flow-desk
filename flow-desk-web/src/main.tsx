import React from 'react';
import ReactDOM from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfigProvider } from 'antd';
import { App } from './app/App';
import './style.css';
const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
ReactDOM.createRoot(document.getElementById('root')!).render(<React.StrictMode><QueryClientProvider client={client}><ConfigProvider theme={{token: {colorPrimary: '#176b57', borderRadius: 10}}}><App/></ConfigProvider></QueryClientProvider></React.StrictMode>);
