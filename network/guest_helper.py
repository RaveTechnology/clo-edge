#!/usr/bin/env python3
"""Restricted SSH entry point. No shell commands or cloud credentials in requests."""
from pathlib import Path
import fcntl,hashlib,json,os,subprocess,sys,time
from guest_network import plan

def command(argv):
 p=subprocess.run(argv,capture_output=True,timeout=5)
 if p.returncode:raise RuntimeError('network command failed')
 return p.stdout

def snapshot():
 links=json.loads(command(['ip','-j','addr']));rules=json.loads(command(['ip','-j','rule']))
 p=subprocess.run(['ip','-j','route','show','table','200'],capture_output=True,timeout=5)
 if p.returncode:
  if b'FIB table does not exist' not in p.stderr:raise RuntimeError('route table unavailable')
  routes=[]
 else:routes=json.loads(p.stdout)
 return links,rules,routes

def main():
 os.umask(0o077);cfg=Path('/etc/clo-edge-network/guest.json');assert cfg.is_file() and not cfg.is_symlink() and cfg.stat().st_mode&0o077==0
 config=json.loads(cfg.read_text());assert Path('/etc/machine-id').read_text().strip()==config['machine_id']
 raw=sys.stdin.buffer.read(65537)
 if len(raw)>65536:raise ValueError('oversized network request')
 request=json.loads(raw);op=request.get('op');assert op in ['inspect','apply'] and type(request.get('expires_at'))in [float,int] and time.time()<=request['expires_at']<=time.time()+45
 lock=Path('/run/clo-edge-network-guest.lock').open('a');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 links,rules,routes=snapshot();mac=request.get('mac','').lower();found=[l for l in links if l.get('address','').lower()==mac]
 if len(found)!=1:raise ValueError('public MAC unavailable')
 rp=Path('/proc/sys/net/ipv4/conf')/found[0]['ifname']/'rp_filter';value=int(rp.read_text())
 result=plan(config,request,links,rules,routes,value);management=result['private_interface'];before_links=[l for l in links if l['ifname']==management];before_routes=command(['ip','-j','route','show','dev',management]);before_main=command(['ip','-j','route','show','table','main']);before_rules=[r for r in rules if r.get('priority')!=32000]
 if op=='apply':
  assert request.get('peer_status')=='STOPPED' and request.get('peer_switch')=='OFF' and request.get('native_owner')==config['server_id']
  for argv in result['commands']:
   current=json.loads(command(['ip','-j','addr']));assert any(l['ifname']==result['interface'] and l.get('address','').lower()==mac for l in current)
   command(argv)
  links,rules,routes=snapshot();assert [l for l in links if l['ifname']==management]==before_links and command(['ip','-j','route','show','dev',management])==before_routes and command(['ip','-j','route','show','table','main'])==before_main and [r for r in rules if r.get('priority')!=32000]==before_rules
  result=plan(config,request,links,rules,routes,int(rp.read_text()));assert result['ready'],'public network did not converge'
 output={'server_id':config['server_id'],'machine_id':config['machine_id'],'op':op,'ready':result['ready'],'interface':result['interface'],'private_unchanged':True,'commands_planned':len(result['commands']),'at':time.time()}
 print(json.dumps(output))
if __name__=='__main__':
 try:main()
 except Exception as error:
  print(json.dumps({'error':type(error).__name__}),file=sys.stderr);sys.exit(1)
