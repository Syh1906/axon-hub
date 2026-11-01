"""保留给人工验收的本地示例数据；只操作同目录独立库。"""
from pathlib import Path
import datetime as dt
import json
import sqlite3

runtime = Path(__file__).resolve().parent / 'runtime'
state = runtime / 'demo-data.json'
if json.loads(state.read_text(encoding='utf-8-sig')).get('phase') == 'complete':
    print('示例记录已存在，保留人工修改。')
    raise SystemExit(0)
c = sqlite3.connect(runtime / 'preview.db')
assert c.execute('select count(*) from requests').fetchone()[0] == 0, '已有记录，不覆盖'
channels = c.execute("select id,name,type from channels where name like '验收 · %' order by id").fetchall()
keys = c.execute("select id,name from api_keys where name like '验收 · %' order by id").fetchall()
assert len(channels) == 2 and len(keys) == 3
now = dt.datetime.now(dt.timezone.utc)
local = now.astimezone(dt.timezone(dt.timedelta(hours=8)))
def stamp(t):
    return t.astimezone(dt.timezone.utc).strftime('%Y-%m-%d %H:%M:%S.%f +0000 UTC')
def put(table, values):
    for key,value in list(values.items()):
        if isinstance(value,(dict,list)):
            values[key] = json.dumps(value,ensure_ascii=False)
    return c.execute('insert into '+table+' ('+','.join(values)+') values ('+','.join('?' for _ in values)+')',list(values.values())).lastrowid
with c:
    group = put('api_key_groups',dict(name='研发团队',note='人工验收示例：包含停用 Key',project_id=1,created_at=stamp(now),updated_at=stamp(now)))
    put('api_key_groups',dict(name='空分组',note='没有 Key 也可打开用量页',project_id=1,created_at=stamp(now),updated_at=stamp(now)))
    c.execute('update api_keys set group_ref=? where id in (?,?)',(group,keys[0][0],keys[2][0]))
    c.execute("update api_keys set status='disabled' where id=?",(keys[2][0],))
    for i in range(27):
        moment = local.replace(hour=9,minute=5+i,second=12,microsecond=0) if i<24 else local.replace(hour=14,minute=i,second=0,microsecond=0)-dt.timedelta(days=1 if i<26 else 8)
        date = stamp(moment)
        ch = channels[i%2]
        key = keys[i%3][0]
        failed = i%6==0
        old = i==23
        status = 'failed' if failed else 'completed'
        model = 'gpt-5.2-codex' if i%2==0 else 'claude-sonnet-4-5'
        body = dict(model=model,messages=[dict(role='user',content='人工验收示例，请说明筛选结果。')],stream=True)
        rid=put('requests',dict(project_id=1,api_key_id=key,channel_id=ch[0],model_id=model,status=status,request_body=body,created_at=date,updated_at=date,stream=True,client_status_code=None if old else 401 if failed else 200,metrics_latency_ms=None if old else 2400,metrics_first_token_latency_ms=None if old else 400))
        attempts = 2 if failed else 1
        for j in range(attempts):
            actual = channels[j] if failed else ch
            code = 429 if failed and j==0 else 401 if failed else 200
            failure = dict(source='upstream_http',code='rate_limit_exceeded' if j==0 else 'invalid_api_key',type='api_error',body=json.dumps(dict(error=dict(message='示例：上游限流' if j==0 else '示例：认证失败',api_key='[REDACTED]')),ensure_ascii=False)) if failed else None
            selection = dict(channel_name=actual[1],provider='openai' if actual[2]=='openai' else 'anthropic',key_hint='****a123' if j==0 else '****b456',key_remark='生产主 Key' if j==0 else '备用 Key',session_state='new' if j==0 else 'switched',strategy='adaptive',priority=10,candidates=[dict(channel_id=x[0],channel_name=x[1],provider='openai' if x[2]=='openai' else 'anthropic',priority=10,models=[model],session_reuse=False) for x in channels])
            put('request_executions',dict(request_id=rid,project_id=1,channel_id=actual[0],model_id=model,request_body=body,status=status,stream=True,created_at=stamp(moment+dt.timedelta(milliseconds=j*500)),updated_at=date,selection=None if old else selection,failure=failure,retry_decision='next_channel' if failed and j==0 else 'candidates_exhausted' if failed else None,response_status_code=None if old else code,error_message='示例上游失败' if failed else None,metrics_latency_ms=None if old else 2400,metrics_first_token_latency_ms=None if old else 400))
        if not failed:
            put('usage_logs',dict(request_id=rid,project_id=1,api_key_id=key,channel_id=ch[0],model_id=model,prompt_tokens=1800,completion_tokens=120,total_tokens=1920,prompt_cached_tokens=900,prompt_write_cached_tokens=300,prompt_write_cached_tokens_5m=300,total_cost=0.0042,created_at=date,updated_at=date))
state.write_text(json.dumps(dict(phase='complete',requests=27,timezone='Asia/Shanghai'),ensure_ascii=False),encoding='utf-8')
print('人工验收数据：今天24条、昨天2条、更早1条；研发团队、空分组、未分组及停用Key。')
