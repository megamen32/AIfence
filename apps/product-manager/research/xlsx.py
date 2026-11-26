import re
import posixpath
from zipfile import ZipFile
from xml.etree import ElementTree as ET

class Workbook:
    """Read only explicitly selected OOXML worksheets and their referenced strings."""
    def close(self):
        self.z.close()

    def __init__(self, source):
        self.z = ZipFile(source)
        self.names = {}
        xl = ET.fromstring(self.z.read('xl/workbook.xml'))
        rel = ET.fromstring(self.z.read('xl/_rels/workbook.xml.rels'))
        targets = {r.get('Id'): r.get('Target') for r in rel}
        for s in xl.findall('.//{*}sheet'):
            target = targets[s.get('{http://schemas.openxmlformats.org/officeDocument/2006/relationships}id')]
            self.names[s.get('name')] = (target.lstrip('/') if target.startswith('/')
                                         else posixpath.normpath(posixpath.join('xl', target)))
    def rows(self, name):
        root = ET.fromstring(self.z.read(self.names[name]))
        rows = {}
        shared = {}
        for r in root.findall('.//{*}sheetData/{*}row'):
            vals = {}
            for c in r.findall('{*}c'):
                ref = c.get('r')
                col = re.match('[A-Z]+', ref).group()
                typ = c.get('t')
                v = c.find('{*}v')
                if typ == 'inlineStr':
                    vals[col] = ''.join(t.text or '' for t in c.findall('.//{*}t'))
                elif v is not None and v.text is not None:
                    if typ == 's':
                        shared.setdefault(int(v.text), []).append((vals, col))
                    elif typ in ('str', 'd'):
                        vals[col] = v.text
                    elif typ != 'e':
                        vals[col] = float(v.text)
            rows[int(r.get('r'))] = vals
        if shared:
            with self.z.open('xl/sharedStrings.xml') as src:
                index = 0
                for _, si in ET.iterparse(src, events=('end',)):
                    if si.tag.rsplit('}', 1)[-1] == 'si':
                        if index in shared:
                            value = ''.join(t.text or '' for t in si.findall('.//{*}t'))
                            for vals, col in shared.pop(index):
                                vals[col] = value
                        index += 1
                        si.clear()
                        if not shared:
                            break
        return rows
