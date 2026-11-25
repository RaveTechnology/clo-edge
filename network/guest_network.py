"""Bounded plans for a reserved IPv4 on its currently attached public NIC."""
import ipaddress,re

def plan(config, request, links, rules, routes, rp_filter):
 def check(ok,message):
  if not ok:raise ValueError(message)
 for field in ['project_id','server_id','machine_id','private_ip','public_ip','prefix','gateway']:
  check(request.get(field)==config[field],'network scope differs: '+field)
 public=ipaddress.IPv4Interface(str(config['public_ip'])+'/'+str(config['prefix']));private=ipaddress.IPv4Address(config['private_ip']);gateway=ipaddress.IPv4Address(config['gateway'])
 check(public.ip!=private and private not in public.network and gateway in public.network and gateway!=public.ip,'invalid public network')
 mac=request.get('mac','').lower();check(bool(re.fullmatch(r'(?:[0-9a-f]{2}:){5}[0-9a-f]{2}',mac)) and not int(mac[:2],16)&1 and mac!='00:00:00:00:00:00','invalid public MAC')
 mgmt=[l for l in links if any(a.get('family')=='inet' and a.get('local')==str(private) for a in l.get('addr_info',[]))];check(len(mgmt)==1 and 'UP'in mgmt[0].get('flags',[]),'private interface identity unavailable')
 candidates=[l for l in links if l.get('address','').lower()==mac];check(len(candidates)==1 and candidates[0]['ifname']!=mgmt[0]['ifname'],'public MAC missing, duplicate or private')
 nic=candidates[0];name=nic['ifname'];check(bool(re.fullmatch('[a-zA-Z0-9_.:-]{1,15}',name)),'invalid interface name')
 check(not any(a.get('family')=='inet' and a.get('local')!=str(public.ip) for a in nic.get('addr_info',[])),'public NIC has an unexpected IPv4')
 check(not any(l['ifname']!=name and any(a.get('family')=='inet' and a.get('local')==str(public.ip) for a in l.get('addr_info',[])) for l in links),'public IPv4 exists on another NIC')
 owned_rules=[r for r in rules if r.get('priority')==32000];check(len(owned_rules)<=1,'duplicate policy rule')
 check(all(str(r.get('table'))=='200' and r.get('src') in [str(public.ip),str(public.ip)+'/32'] for r in owned_rules),'reserved routing priority belongs to another rule')
 network=str(public.network)
 def owned_route(r):return r.get('dev')==name and r.get('prefsrc')==str(public.ip) and ((r.get('dst')==network and not r.get('gateway')) or (r.get('dst')=='default' and r.get('gateway')==str(gateway)))
 check(all(owned_route(r) for r in routes),'reserved route table contains unknown routes')
 commands=[]
 if 'UP'not in nic.get('flags',[]):commands.append(['ip','link','set','dev',name,'up'])
 addresses=[a for a in nic.get('addr_info',[]) if a.get('family')=='inet' and a.get('local')==str(public.ip)]
 check(all(a.get('prefixlen')==public.network.prefixlen for a in addresses),'existing public prefix differs')
 if not addresses:commands.append(['ip','addr','add',str(public),'dev',name,'noprefixroute'])
 if not any(r.get('dst')==network for r in routes):commands.append(['ip','route','replace',network,'dev',name,'scope','link','src',str(public.ip),'table','200'])
 if not any(r.get('dst')=='default' for r in routes):commands.append(['ip','route','replace','default','via',str(gateway),'dev',name,'src',str(public.ip),'table','200'])
 if not owned_rules:commands.append(['ip','rule','add','priority','32000','from',str(public.ip)+'/32','lookup','200'])
 if rp_filter!=2:commands.append(['sysctl','-w','net.ipv4.conf.'+name+'.rp_filter=2'])
 return {'interface':name,'private_interface':mgmt[0]['ifname'],'commands':commands,'ready':not commands}
