#!/usr/bin/env python3
"""Read-only cloud observer that repairs the fenced transfer target via restricted SSH."""
from pathlib import Path
import fcntl,hashlib,ipaddress,json,os,subprocess,sys,time,urllib.request,urllib.error,uuid,tempfile

def select_target(config, state, address, servers):
 if state['machine_id']!=config['controller_machine_id'] or state['config_hash']!=config['core_config_hash']:raise ValueError('controller binding differs')
 if address['id']!=config['address_id'] or address['address']!=config['public_ip'] or address['type']!='FIXED' or address['status']!='ACTIVE':raise ValueError('public address not terminal')
 owner=(address.get('attached_to')or{}).get('id');edges={e['server_id']:e for e in config['edges']}
 if owner not in edges or len(edges)!=2 or address['attached_to']['entity']!='server':raise ValueError('public owner outside dedicated scope')
 target=servers[owner];peer=servers[next(k for k in edges if k!=owner)]
 if target['project']!=config['project_id'] or peer['project']!=config['project_id'] or target['status']!='ACTIVE' or target['switch_status']!='ON':raise ValueError('target identity/power differs')
 prefix=ipaddress.IPv4Network('0.0.0.0/'+address['mask']).prefixlen
 if prefix!=config['prefix'] or address['gateway']!=config['gateway']:raise ValueError('public network differs')
 mac=address['mac_addr'];edge=edges[owner]
 payload={k:edge[k] for k in ['server_id','machine_id','private_ip']};payload.update(project_id=config['project_id'],public_ip=config['public_ip'],prefix=prefix,gateway=config['gateway'],mac=mac,nonce=str(uuid.uuid4()))
 pending=state.get('pending');phase=state.get('phase');accepted=isinstance(pending,dict) and pending.get('kind')=='attach' and pending.get('accepted')is True
 safe_pending=(phase in ['Attaching','RecoveryRequired'] and accepted) or (phase in ['Verifying','Completed'] and pending is None)
 fenced=peer['status']=='STOPPED' and peer['switch_status']=='OFF';workflow=state.get('source_id')==peer['id'] and state.get('target_id')==owner and (phase!='Completed' or state.get('active_id')==owner)
 return edge,payload,fenced and workflow and safe_pending

def cache_allowed(config,state,last,now):
 known=last.get('ready')is True or (last.get('ready')is False and last.get('blocked_fence_required')is True and last.get('core_phase')==state.get('phase'))
 return state.get('machine_id')==config['controller_machine_id'] and state.get('config_hash')==config['core_config_hash'] and state.get('phase')in ['Watching','Completed'] and not state.get('pending') and known and last.get('server_id')==state.get('active_id') and isinstance(last.get('at'),(int,float)) and 0<=now-last['at']<60

def network_observation_needed(state):
 return state.get('phase')in ['Watching','Attaching','Verifying','RecoveryRequired','Completed']

def workflow_unchanged(before,after):
 return all(before.get(k)==after.get(k)for k in ['machine_id','config_hash','source_id','target_id','active_id','phase','phase_since','pending'])

def read_cache(path):
 try:
  value=json.loads(path.read_text());return value if isinstance(value,dict)else{}
 except (OSError,ValueError,TypeError):return{}

def write_cache(path,value):
 temporary=None
 try:
  with tempfile.NamedTemporaryFile(mode='w',dir=path.parent,prefix='.state-',delete=False)as f:
   temporary=Path(f.name);json.dump(value,f,indent=2);f.flush();os.fsync(f.fileno())
  os.replace(temporary,path);temporary=None;fd=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY)
  try:os.fsync(fd)
  finally:os.close(fd)
 finally:
  if temporary is not None:temporary.unlink(missing_ok=True)

def main():
 os.umask(0o077);path=Path(sys.argv[1]);assert path.is_file() and not path.is_symlink() and path.stat().st_mode&0o077==0;c=json.loads(path.read_text());assert Path('/etc/machine-id').read_text().strip()==c['controller_machine_id'];root=Path('/var/lib/clo-edge-network');root.mkdir(mode=0o700,exist_ok=True);lock=(root/'lock').open('a');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 assert hashlib.sha256(Path(c['core_config_path']).read_bytes()).hexdigest()==c['core_config_file_sha256']
 st=json.loads(subprocess.check_output(['/usr/local/bin/clo-edge','inspect','--state-dir',c['core_state_dir']],stderr=subprocess.PIPE,timeout=5));token=Path(c['token_file']).read_text().strip()
 if not network_observation_needed(st):
  print(json.dumps({'skipped':True,'core_phase':st.get('phase')}));return
 lastfile=root/'state.json'
 cached=read_cache(lastfile)
 if cache_allowed(c,st,cached,time.time()):
  if cached.get('ready')is not True:raise ValueError('fence required; cloud inspection backoff')
  print(json.dumps({'ready':True,'cached':True}));return
 class NoRedirect(urllib.request.HTTPRedirectHandler):
  def redirect_request(self,*a,**kw):return None
 opener=urllib.request.build_opener(NoRedirect())
 def fetch(kind):
  q=urllib.request.Request('https://api.clo.ru/v2/projects/'+c['project_id']+'/'+kind+'?limit=100&offset=0',headers={'Authorization':'Bearer '+token})
  for attempt in range(3):
   try:
    with opener.open(q,timeout=8)as r:x=json.load(r)
    break
   except urllib.error.HTTPError as error:
    if error.code!=429 or attempt==2:raise
    try:delay=max(1,min(5,int(error.headers.get('Retry-After','2'))))
    except ValueError:delay=2
    time.sleep(delay)
  time.sleep(0.25)
  if x['count']!=len(x['result']) or len({i['id']for i in x['result']})!=x['count']:raise ValueError('incomplete cloud inventory')
  return x['result']
 addresses=fetch('addresses');servers={s['id']:s for s in fetch('servers')};address=next(a for a in addresses if a['id']==c['address_id']);edge,payload,may_apply=select_target(c,st,address,servers)
 def guest(op):
  request=dict(payload,op=op,expires_at=time.time()+25,native_owner=edge['server_id']);peer=next(v for k,v in servers.items()if k in {e['server_id']for e in c['edges']}and k!=edge['server_id']);request.update(peer_status=peer['status'],peer_switch=peer['switch_status']);argv=['ssh','-F','/dev/null','-i',c['ssh_key_file'],'-o','IdentitiesOnly=yes','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+c['known_hosts_file'],'-o','ConnectTimeout=3','-o','ConnectionAttempts=1','root@'+edge['private_ip']];p=subprocess.run(argv,input=json.dumps(request).encode(),capture_output=True,timeout=12)
  if p.returncode:raise RuntimeError('restricted guest action failed')
  reply=json.loads(p.stdout)
  if reply['server_id']!=edge['server_id']or reply['machine_id']!=edge['machine_id']:raise ValueError('guest binding differs')
  return reply
 result=guest('inspect');applied=False
 if not result['ready']:
  if not may_apply:
   write_cache(root/'state.json',{'at':time.time(),'server_id':result['server_id'],'ready':False,'blocked_fence_required':True,'core_phase':st['phase']})
   raise ValueError('network mutation requires a terminal fenced transfer')
  newer={s['id']:s for s in fetch('servers')};again=next(a for a in fetch('addresses')if a['id']==c['address_id']);e2,p2,safe=select_target(c,st,again,newer)
  if not safe or e2!=edge or p2['mac']!=payload['mac']:raise ValueError('cloud transfer changed before apply')
  current=json.loads(subprocess.check_output(['/usr/local/bin/clo-edge','inspect','--state-dir',c['core_state_dir']],stderr=subprocess.PIPE,timeout=5))
  if not workflow_unchanged(st,current):raise ValueError('core workflow changed before apply')
  result=guest('apply');applied=True
 write_cache(root/'state.json',{'at':time.time(),'server_id':result['server_id'],'ready':result['ready'],'private_unchanged':result['private_unchanged'],'core_phase':st['phase'],'applied':applied})
 print(json.dumps({'ready':result['ready'],'server_id':result['server_id']}))
if __name__=='__main__':
 try:main()
 except Exception as error:
  print(json.dumps({'error':type(error).__name__}),file=sys.stderr);sys.exit(1)
