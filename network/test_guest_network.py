import copy,unittest
from guest_network import plan
class NetworkPlanTests(unittest.TestCase):
 def setUp(self):
  self.c={'project_id':'project','server_id':'target','machine_id':'host','private_ip':'10.0.0.10','public_ip':'192.0.2.20','prefix':24,'gateway':'192.0.2.1'}
  self.q={**self.c,'mac':'02:11:22:33:44:55'}
  self.links=[{'ifname':'priv0','address':'02:aa:aa:aa:aa:aa','flags':['UP'],'addr_info':[{'family':'inet','local':'10.0.0.10','prefixlen':24}]},{'ifname':'pub7','address':self.q['mac'],'flags':[],'addr_info':[]}];self.rules=[];self.routes=[]
 def run_plan(self):return plan(self.c,self.q,self.links,self.rules,self.routes,1)
 def test_new_public_interface_plan(self):
  x=self.run_plan();self.assertEqual(x['interface'],'pub7');self.assertIn(['ip','link','set','dev','pub7','up'],x['commands']);self.assertIn(['ip','addr','add','192.0.2.20/24','dev','pub7','noprefixroute'],x['commands']);self.assertTrue(all('priv0' not in c for c in x['commands']))
 def test_ready_network_is_idempotent(self):
  self.links[1]['flags']=['UP'];self.links[1]['addr_info']=[{'family':'inet','local':self.c['public_ip'],'prefixlen':24}];self.rules=[{'priority':32000,'src':self.c['public_ip'],'table':'200'}];self.routes=[{'dst':'192.0.2.0/24','dev':'pub7','scope':'link','prefsrc':self.c['public_ip']},{'dst':'default','gateway':self.c['gateway'],'dev':'pub7','prefsrc':self.c['public_ip']}]
  self.assertEqual(plan(self.c,self.q,self.links,self.rules,self.routes,2)['commands'],[])
 def test_unsafe_scope_or_network_is_rejected(self):
  for kind in ['server','machine','address','private_mac','duplicate_mac','wrong_address','rule','route']:
   with self.subTest(kind=kind):
    self.setUp()
    if kind=='server':self.q['server_id']='different'
    if kind=='machine':self.q['machine_id']='different'
    if kind=='address':self.q['public_ip']='192.0.2.99'
    if kind=='private_mac':self.q['mac']=self.links[0]['address']
    if kind=='duplicate_mac':self.links.append(copy.deepcopy(self.links[1]))
    if kind=='wrong_address':self.links[1]['addr_info']=[{'family':'inet','local':'198.51.100.1','prefixlen':24}]
    if kind=='rule':self.rules=[{'priority':32000,'src':'203.0.113.1','table':'200'}]
    if kind=='route':self.routes=[{'dst':'default','gateway':'203.0.113.1','dev':'pub7'}]
    with self.assertRaises(ValueError):self.run_plan()
if __name__=='__main__':unittest.main()
