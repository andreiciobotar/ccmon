"""Convert truecolor ANSI text on stdin to an HTML page on stdout (for `make screenshot`)."""
import re,sys,html,unicodedata
data=sys.stdin.read()
out=[];st={}
def span(t):
    if not t: return
    css=[]
    if 'fg' in st: css.append('color:rgb(%s)'%st['fg'])
    if 'bg' in st: css.append('background:rgb(%s)'%st['bg'])
    if st.get('b'): css.append('font-weight:bold')
    if st.get('f'): css.append('opacity:.6')
    if st.get('i'): css.append('font-style:italic')
    if st.get('u'): css.append('text-decoration:underline')
    # a double-width character takes exactly two cells, whatever the fallback font's advance
    body=''.join('<span style="display:inline-block;width:2ch;text-align:center">%s</span>'%c
                 if unicodedata.east_asian_width(c) in 'WF' else html.escape(c) for c in t)
    out.append('<span style="%s">%s</span>'%(';'.join(css),body))
pos=0
for m in re.finditer(r'\x1b\[([0-9;]*)m',data):
    span(data[pos:m.start()]); pos=m.end()
    p=[int(x) if x else 0 for x in m.group(1).split(';')] if m.group(1) else [0]
    i=0
    while i<len(p):
        c=p[i]
        if c==0: st.clear()
        elif c==1: st['b']=1
        elif c==2: st['f']=1
        elif c==3: st['i']=1
        elif c==4: st['u']=1
        elif c==22: st.pop('b',0);st.pop('f',0)
        elif c in(38,48) and i+4<len(p) and p[i+1]==2:
            st['fg' if c==38 else 'bg']='%d,%d,%d'%tuple(p[i+2:i+5]); i+=4
        elif c==39: st.pop('fg',0)
        elif c==49: st.pop('bg',0)
        i+=1
span(data[pos:])
print('<html><body style="margin:0;background:#12111C"><pre style="margin:0;padding:8px;font:14px Menlo,monospace;color:#ECEAF7;display:inline-block">'+''.join(out)+'</pre></body></html>')
